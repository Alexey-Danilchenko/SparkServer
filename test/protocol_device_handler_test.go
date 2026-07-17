// Package test verifies device protocol handler behavior.
package test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"net"
	"path/filepath"
	"testing"
	"time"

	"sparkserver/internal/devices"
	"sparkserver/internal/domain"
	"sparkserver/internal/events"
	"sparkserver/internal/protocol/coap"
	protocoldevice "sparkserver/internal/protocol/device"
	"sparkserver/internal/protocol/framing"
	"sparkserver/internal/protocol/session"
	"sparkserver/internal/protocol/tcp"
	filerepo "sparkserver/internal/repository/file"
)

func TestDeviceHandlerPublishesEventPacket(t *testing.T) {
	eventService := events.NewService(nil)
	handler := protocoldevice.NewHandler(eventService)
	deviceSession := &session.Session{DeviceID: "device-1", SessionKey: []byte("0123456789abcdef")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := eventService.Subscribe(ctx, events.Filter{DeviceID: "device-1"})
	response, err := handler.Handle(context.Background(), deviceSession, &coap.Packet{
		Type:      coap.Confirmable,
		Code:      coap.CodePost,
		MessageID: 11,
		Token:     []byte{0x01},
		Options: []coap.Option{
			{Number: coap.OptionURIPath, Value: []byte("events")},
			{Number: coap.OptionURIPath, Value: []byte("brew.started")},
		},
		Payload: []byte("hot"),
	})
	if err != nil {
		t.Fatalf("handle event packet: %v", err)
	}
	if response == nil || response.Code != coap.CodeChanged || response.MessageID != 11 {
		t.Fatalf("response = %#v", response)
	}

	select {
	case event := <-received:
		if event.Name != "brew.started" || event.Data != "hot" || event.DeviceID != "device-1" {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestDeviceHandlerStoresDeviceDescription(t *testing.T) {
	dir := t.TempDir()
	deviceService := devices.NewService(
		filerepo.NewDeviceRepository(filepath.Join(dir, "devices")),
		filerepo.NewDeviceClaimRepository(filepath.Join(dir, "deviceClaims")),
	)
	if _, err := deviceService.Claim(context.Background(), "owner-1", "device-1"); err != nil {
		t.Fatalf("claim device: %v", err)
	}

	handler := protocoldevice.NewHandler(nil, deviceService)
	deviceSession := &session.Session{DeviceID: "device-1", SessionKey: []byte("0123456789abcdef")}
	response, err := handler.Handle(context.Background(), deviceSession, &coap.Packet{
		Type:      coap.Confirmable,
		Code:      coap.CodePost,
		MessageID: 22,
		Options: []coap.Option{
			{Number: coap.OptionURIPath, Value: []byte("describe")},
		},
		Payload: []byte(`{"variables":{"temperature":"double","ready":{"type":"bool"}},"functions":["brew","stop"],"attributes":{"firmware_version":"1.2.3"}}`),
	})
	if err != nil {
		t.Fatalf("handle description packet: %v", err)
	}
	if response == nil || response.Code != coap.CodeChanged || response.MessageID != 22 {
		t.Fatalf("response = %#v", response)
	}

	device, err := deviceService.Get(context.Background(), "owner-1", "device-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if !device.Connected {
		t.Fatal("device was not marked connected")
	}
	if device.Variables["temperature"] != "double" || device.Variables["ready"] != "bool" {
		t.Fatalf("variables = %#v", device.Variables)
	}
	if len(device.Functions) != 2 || device.Functions[0] != "brew" || device.Functions[1] != "stop" {
		t.Fatalf("functions = %#v", device.Functions)
	}
	if device.Attributes["firmware_version"] != "1.2.3" {
		t.Fatalf("attributes = %#v", device.Attributes)
	}
}

func TestDeviceHandlerStoresParticleAliasDescription(t *testing.T) {
	dir := t.TempDir()
	deviceService := devices.NewService(
		filerepo.NewDeviceRepository(filepath.Join(dir, "devices")),
		filerepo.NewDeviceClaimRepository(filepath.Join(dir, "deviceClaims")),
	)

	handler := protocoldevice.NewHandler(nil, deviceService)
	deviceSession := &session.Session{DeviceID: "device-2", SessionKey: []byte("0123456789abcdef")}
	_, err := handler.Handle(context.Background(), deviceSession, &coap.Packet{
		Type:      coap.Confirmable,
		Code:      coap.CodePut,
		MessageID: 23,
		Options: []coap.Option{
			{Number: coap.OptionURIPath, Value: []byte("d")},
		},
		Payload: []byte(`{"v":[{"name":"humidity","type":"double"}],"f":[{"name":"mist"}],"cc3000_patch_version":"1.29","product_id":6}`),
	})
	if err != nil {
		t.Fatalf("handle alias description packet: %v", err)
	}

	device, err := deviceService.Get(context.Background(), "", "device-2")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if device.Variables["humidity"] != "double" {
		t.Fatalf("variables = %#v", device.Variables)
	}
	if len(device.Functions) != 1 || device.Functions[0] != "mist" {
		t.Fatalf("functions = %#v", device.Functions)
	}
	if device.Attributes["cc3000_patch_version"] != "1.29" || device.Attributes["product_id"] != "6" {
		t.Fatalf("attributes = %#v", device.Attributes)
	}
	if device.ProductID != "6" {
		t.Fatalf("product id = %q", device.ProductID)
	}
}

func TestDeviceHandlerChecksFirmwareAfterDescriptionResponse(t *testing.T) {
	dir := t.TempDir()
	deviceService := devices.NewService(
		filerepo.NewDeviceRepository(filepath.Join(dir, "devices")),
		filerepo.NewDeviceClaimRepository(filepath.Join(dir, "deviceClaims")),
	)
	updater := &fakeDeviceFirmwareUpdater{devices: make(chan *domain.Device, 1)}

	handler := protocoldevice.NewHandler(nil, deviceService)
	handler.SetFirmwareUpdater(updater)
	deviceSession := &session.Session{DeviceID: "device-3", SessionKey: []byte("0123456789abcdef")}
	packet := &coap.Packet{
		Type:      coap.Confirmable,
		Code:      coap.CodePut,
		MessageID: 24,
		Options: []coap.Option{
			{Number: coap.OptionURIPath, Value: []byte("d")},
		},
		Payload: []byte(`{"product_id":"product-1","product_firmware_version":1}`),
	}
	if _, err := handler.Handle(context.Background(), deviceSession, packet); err != nil {
		t.Fatalf("handle description: %v", err)
	}

	select {
	case <-updater.devices:
		t.Fatal("firmware update ran before response hook")
	default:
	}

	handler.AfterResponse(context.Background(), deviceSession, packet)
	select {
	case device := <-updater.devices:
		if device.ID != "device-3" || device.ProductID != "product-1" {
			t.Fatalf("device = %#v", device)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for firmware update hook")
	}
}

func TestTCPServeSessionDecryptsCoAPAndWritesResponse(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	deviceSession := &session.Session{DeviceID: "device-1", SessionKey: []byte("0123456789abcdef")}
	codec, err := session.NewCodecFromSession(deviceSession)
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}

	handler := protocoldevice.NewHandler(events.NewService(nil))
	tcpClient, err := tcp.NewClient(deviceSession, server)
	if err != nil {
		t.Fatalf("new tcp client: %v", err)
	}
	touched := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- tcp.ServeSession(context.Background(), server, deviceSession, tcpClient, handler, func(deviceID string) {
			touched <- deviceID
		})
	}()

	if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	requestPlaintext, err := coap.Marshal(coap.Packet{
		Type:      coap.Confirmable,
		Code:      coap.CodeGet,
		MessageID: 99,
		Token:     []byte{0xaa},
		Options: []coap.Option{
			{Number: coap.OptionURIPath, Value: []byte("ping")},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	requestFrame, err := codec.Encrypt(requestPlaintext)
	if err != nil {
		t.Fatalf("encrypt request: %v", err)
	}
	if err := framing.NewWriter(client).WriteFrame(requestFrame); err != nil {
		t.Fatalf("write request frame: %v", err)
	}

	responseFrame, err := framing.NewReader(client, framing.DefaultMaxFrameSize).ReadFrame()
	if err != nil {
		t.Fatalf("read response frame: %v", err)
	}
	responsePlaintext, err := codec.Decrypt(responseFrame)
	if err != nil {
		t.Fatalf("decrypt response: %v", err)
	}
	response, err := coap.Parse(responsePlaintext)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if response.Type != coap.Acknowledgement || response.Code != coap.CodeChanged || response.MessageID != 99 || !bytes.Equal(response.Token, []byte{0xaa}) {
		t.Fatalf("response = %#v", response)
	}

	select {
	case deviceID := <-touched:
		if deviceID != "device-1" {
			t.Fatalf("touched device id = %q", deviceID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for touch callback")
	}

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serve session did not stop")
	}
}

func TestTCPClientGetVariableRoundTrip(t *testing.T) {
	device, server := net.Pipe()
	defer device.Close()
	defer server.Close()

	deviceSession := &session.Session{DeviceID: "device-1", SessionKey: []byte("0123456789abcdef")}
	tcpClient, err := tcp.NewClient(deviceSession, server)
	if err != nil {
		t.Fatalf("new tcp client: %v", err)
	}
	codec, err := session.NewCodecFromSession(deviceSession)
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}

	replyPumpDone := make(chan error, 1)
	go func() {
		responseFrame, err := framing.NewReader(server, framing.DefaultMaxFrameSize).ReadFrame()
		if err != nil {
			replyPumpDone <- err
			return
		}
		responsePlaintext, err := codec.Decrypt(responseFrame)
		if err != nil {
			replyPumpDone <- err
			return
		}
		response, err := coap.Parse(responsePlaintext)
		if err != nil {
			replyPumpDone <- err
			return
		}
		tcpClient.HandlePacket(response)
		replyPumpDone <- nil
	}()

	go func() {
		requestFrame, err := framing.NewReader(device, framing.DefaultMaxFrameSize).ReadFrame()
		if err != nil {
			t.Errorf("read request frame: %v", err)
			return
		}
		requestPlaintext, err := codec.Decrypt(requestFrame)
		if err != nil {
			t.Errorf("decrypt request: %v", err)
			return
		}
		request, err := coap.Parse(requestPlaintext)
		if err != nil {
			t.Errorf("parse request: %v", err)
			return
		}
		if request.Path() != "variable/temperature" {
			t.Errorf("path = %q", request.Path())
			return
		}

		responsePlaintext, err := coap.Marshal(coap.Packet{
			Type:      coap.Acknowledgement,
			Code:      coap.CodeContent,
			MessageID: request.MessageID,
			Token:     request.Token,
			Payload:   []byte(`{"result":21.5}`),
		})
		if err != nil {
			t.Errorf("marshal response: %v", err)
			return
		}
		responseFrame, err := codec.Encrypt(responsePlaintext)
		if err != nil {
			t.Errorf("encrypt response: %v", err)
			return
		}
		if err := framing.NewWriter(device).WriteFrame(responseFrame); err != nil {
			t.Errorf("write response frame: %v", err)
		}
	}()

	value, err := tcpClient.GetVariable(context.Background(), "temperature")
	if err != nil {
		t.Fatalf("get variable: %v", err)
	}
	if value != "21.5" {
		t.Fatalf("value = %q", value)
	}

	select {
	case err := <-replyPumpDone:
		if err != nil {
			t.Fatalf("reply pump: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reply pump")
	}
}

func TestTCPClientCallFunctionUsesParticlePathAndQuery(t *testing.T) {
	device, server := net.Pipe()
	defer device.Close()
	defer server.Close()

	deviceSession := &session.Session{DeviceID: "device-1", SessionKey: []byte("0123456789abcdef")}
	tcpClient, err := tcp.NewClient(deviceSession, server)
	if err != nil {
		t.Fatalf("new tcp client: %v", err)
	}
	codec, err := session.NewCodecFromSession(deviceSession)
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}

	replyPumpDone := make(chan error, 1)
	go func() {
		responseFrame, err := framing.NewReader(server, framing.DefaultMaxFrameSize).ReadFrame()
		if err != nil {
			replyPumpDone <- err
			return
		}
		responsePlaintext, err := codec.Decrypt(responseFrame)
		if err != nil {
			replyPumpDone <- err
			return
		}
		response, err := coap.Parse(responsePlaintext)
		if err != nil {
			replyPumpDone <- err
			return
		}
		tcpClient.HandlePacket(response)
		replyPumpDone <- nil
	}()

	go func() {
		requestFrame, err := framing.NewReader(device, framing.DefaultMaxFrameSize).ReadFrame()
		if err != nil {
			t.Errorf("read request frame: %v", err)
			return
		}
		requestPlaintext, err := codec.Decrypt(requestFrame)
		if err != nil {
			t.Errorf("decrypt request: %v", err)
			return
		}
		request, err := coap.Parse(requestPlaintext)
		if err != nil {
			t.Errorf("parse request: %v", err)
			return
		}
		if request.Path() != "function/brew" {
			t.Errorf("path = %q", request.Path())
			return
		}
		if request.QueryValues().Get("arg") != "start" {
			t.Errorf("query = %#v", request.QueryValues())
			return
		}
		if string(request.Payload) != "start" {
			t.Errorf("payload = %q", request.Payload)
			return
		}

		responsePlaintext, err := coap.Marshal(coap.Packet{
			Type:      coap.Acknowledgement,
			Code:      coap.CodeChanged,
			MessageID: request.MessageID,
			Token:     request.Token,
			Payload:   []byte(`{"return_value":7}`),
		})
		if err != nil {
			t.Errorf("marshal response: %v", err)
			return
		}
		responseFrame, err := codec.Encrypt(responsePlaintext)
		if err != nil {
			t.Errorf("encrypt response: %v", err)
			return
		}
		if err := framing.NewWriter(device).WriteFrame(responseFrame); err != nil {
			t.Errorf("write response frame: %v", err)
		}
	}()

	value, err := tcpClient.CallFunction(context.Background(), "brew", "start")
	if err != nil {
		t.Fatalf("call function: %v", err)
	}
	if value != 7 {
		t.Fatalf("return value = %d", value)
	}

	select {
	case err := <-replyPumpDone:
		if err != nil {
			t.Fatalf("reply pump: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reply pump")
	}
}

func TestTCPClientRequestTimeout(t *testing.T) {
	deviceSession := &session.Session{DeviceID: "device-1", SessionKey: []byte("0123456789abcdef")}
	tcpClient, err := tcp.NewClient(deviceSession, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("new tcp client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err = tcpClient.GetVariable(ctx, "temperature")
	if !errors.Is(err, tcp.ErrDeviceTimeout) {
		t.Fatalf("get variable error = %v", err)
	}
}

func TestTCPClientBeginFlashUsesOTABeginPacket(t *testing.T) {
	device, server := net.Pipe()
	defer device.Close()
	defer server.Close()

	deviceSession := &session.Session{DeviceID: "device-1", SessionKey: []byte("0123456789abcdef")}
	tcpClient, err := tcp.NewClient(deviceSession, server)
	if err != nil {
		t.Fatalf("new tcp client: %v", err)
	}
	codec, err := session.NewCodecFromSession(deviceSession)
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}

	replyPumpDone := make(chan error, 1)
	go func() {
		responseFrame, err := framing.NewReader(server, framing.DefaultMaxFrameSize).ReadFrame()
		if err != nil {
			replyPumpDone <- err
			return
		}
		responsePlaintext, err := codec.Decrypt(responseFrame)
		if err != nil {
			replyPumpDone <- err
			return
		}
		response, err := coap.Parse(responsePlaintext)
		if err != nil {
			replyPumpDone <- err
			return
		}
		tcpClient.HandlePacket(response)
		replyPumpDone <- nil
	}()

	go func() {
		requestFrame, err := framing.NewReader(device, framing.DefaultMaxFrameSize).ReadFrame()
		if err != nil {
			t.Errorf("read request frame: %v", err)
			return
		}
		requestPlaintext, err := codec.Decrypt(requestFrame)
		if err != nil {
			t.Errorf("decrypt request: %v", err)
			return
		}
		request, err := coap.Parse(requestPlaintext)
		if err != nil {
			t.Errorf("parse request: %v", err)
			return
		}
		if request.Path() != "u" {
			t.Errorf("path = %q", request.Path())
			return
		}

		if len(request.Payload) != 12 {
			t.Errorf("payload length = %d", len(request.Payload))
			return
		}
		if request.Payload[0] != 1 ||
			binary.BigEndian.Uint16(request.Payload[1:3]) != 512 ||
			binary.BigEndian.Uint32(request.Payload[3:7]) != 1200 ||
			request.Payload[7] != 0 ||
			binary.BigEndian.Uint32(request.Payload[8:12]) != 0 {
			t.Errorf("payload = %x", request.Payload)
			return
		}

		responsePlaintext, err := coap.Marshal(coap.Packet{
			Type:      coap.Acknowledgement,
			Code:      coap.CodeChanged,
			MessageID: request.MessageID,
			Token:     request.Token,
			Payload:   []byte{1},
		})
		if err != nil {
			t.Errorf("marshal response: %v", err)
			return
		}
		responseFrame, err := codec.Encrypt(responsePlaintext)
		if err != nil {
			t.Errorf("encrypt response: %v", err)
			return
		}
		if err := framing.NewWriter(device).WriteFrame(responseFrame); err != nil {
			t.Errorf("write response frame: %v", err)
		}
	}()

	err = tcpClient.BeginFlash(context.Background(), &domain.FlashJob{
		ID:              "job-1",
		FirmwareID:      "firmware-1",
		FirmwareVersion: 7,
		Size:            1200,
		SHA256:          "abc123",
		ChunkSize:       512,
		ChunkCount:      3,
	})
	if err != nil {
		t.Fatalf("begin flash: %v", err)
	}

	select {
	case err := <-replyPumpDone:
		if err != nil {
			t.Fatalf("reply pump: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reply pump")
	}
}

func TestTCPClientSendFlashChunkUsesOTAChunkPacket(t *testing.T) {
	device, server := net.Pipe()
	defer device.Close()
	defer server.Close()

	deviceSession := &session.Session{DeviceID: "device-1", SessionKey: []byte("0123456789abcdef")}
	tcpClient, err := tcp.NewClient(deviceSession, server)
	if err != nil {
		t.Fatalf("new tcp client: %v", err)
	}
	codec, err := session.NewCodecFromSession(deviceSession)
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}

	replyPumpDone := make(chan error, 1)
	go func() {
		reader := framing.NewReader(server, framing.DefaultMaxFrameSize)
		for index := 0; index < 2; index++ {
			responseFrame, err := reader.ReadFrame()
			if err != nil {
				replyPumpDone <- err
				return
			}
			responsePlaintext, err := codec.Decrypt(responseFrame)
			if err != nil {
				replyPumpDone <- err
				return
			}
			response, err := coap.Parse(responsePlaintext)
			if err != nil {
				replyPumpDone <- err
				return
			}
			tcpClient.HandlePacket(response)
		}
		replyPumpDone <- nil
	}()

	go func() {
		reader := framing.NewReader(device, framing.DefaultMaxFrameSize)
		writer := framing.NewWriter(device)

		beginFrame, err := reader.ReadFrame()
		if err != nil {
			t.Errorf("read begin frame: %v", err)
			return
		}
		beginPlaintext, err := codec.Decrypt(beginFrame)
		if err != nil {
			t.Errorf("decrypt begin: %v", err)
			return
		}
		begin, err := coap.Parse(beginPlaintext)
		if err != nil {
			t.Errorf("parse begin: %v", err)
			return
		}
		if begin.Path() != "u" {
			t.Errorf("begin path = %q", begin.Path())
			return
		}
		beginResponsePlaintext, err := coap.Marshal(coap.Packet{
			Type:      coap.Acknowledgement,
			Code:      coap.CodeChanged,
			MessageID: begin.MessageID,
			Token:     begin.Token,
			Payload:   []byte{1},
		})
		if err != nil {
			t.Errorf("marshal begin response: %v", err)
			return
		}
		beginResponseFrame, err := codec.Encrypt(beginResponsePlaintext)
		if err != nil {
			t.Errorf("encrypt begin response: %v", err)
			return
		}
		if err := writer.WriteFrame(beginResponseFrame); err != nil {
			t.Errorf("write begin response frame: %v", err)
			return
		}

		chunkFrame, err := reader.ReadFrame()
		if err != nil {
			t.Errorf("read chunk frame: %v", err)
			return
		}
		chunkPlaintext, err := codec.Decrypt(chunkFrame)
		if err != nil {
			t.Errorf("decrypt chunk: %v", err)
			return
		}
		chunkRequest, err := coap.Parse(chunkPlaintext)
		if err != nil {
			t.Errorf("parse chunk: %v", err)
			return
		}
		if chunkRequest.Path() != "c" {
			t.Errorf("path = %q", chunkRequest.Path())
			return
		}

		if len(chunkRequest.Payload) != 5 || !bytes.Equal(chunkRequest.Payload[:3], []byte{0x01, 0x02, 0x03}) || !bytes.Equal(chunkRequest.Payload[3:], []byte{0x00, 0x00}) {
			t.Errorf("payload = %x", chunkRequest.Payload)
			return
		}

		queryOptions := chunkQueryOptions(chunkRequest)
		expectedCRC := crc32.ChecksumIEEE(chunkRequest.Payload)
		if len(queryOptions) != 2 ||
			binary.BigEndian.Uint32(queryOptions[0]) != expectedCRC ||
			binary.BigEndian.Uint16(queryOptions[1]) != 2 {
			t.Errorf("query options = %x crc=%08x", queryOptions, expectedCRC)
			return
		}

		responsePlaintext, err := coap.Marshal(coap.Packet{
			Type:      coap.Acknowledgement,
			Code:      coap.CodeChanged,
			MessageID: chunkRequest.MessageID,
			Token:     chunkRequest.Token,
			Payload:   queryOptions[0],
		})
		if err != nil {
			t.Errorf("marshal response: %v", err)
			return
		}
		responseFrame, err := codec.Encrypt(responsePlaintext)
		if err != nil {
			t.Errorf("encrypt response: %v", err)
			return
		}
		if err := writer.WriteFrame(responseFrame); err != nil {
			t.Errorf("write response frame: %v", err)
		}
	}()

	err = tcpClient.BeginFlash(context.Background(), &domain.FlashJob{
		ID:        "job-1",
		Size:      5,
		ChunkSize: 5,
	})
	if err != nil {
		t.Fatalf("begin flash: %v", err)
	}

	err = tcpClient.SendFlashChunk(
		context.Background(),
		&domain.FlashJob{ID: "job-1", ChunkSize: 5},
		domain.OTAChunk{Index: 2, Offset: 1024, Size: 3, SHA256: "chunk-sha"},
		[]byte{0x01, 0x02, 0x03},
	)
	if err != nil {
		t.Fatalf("send flash chunk: %v", err)
	}

	select {
	case err := <-replyPumpDone:
		if err != nil {
			t.Fatalf("reply pump: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reply pump")
	}
}

func TestTCPClientCompleteFlashUsesUpdateDonePacket(t *testing.T) {
	device, server := net.Pipe()
	defer device.Close()
	defer server.Close()

	deviceSession := &session.Session{DeviceID: "device-1", SessionKey: []byte("0123456789abcdef")}
	tcpClient, err := tcp.NewClient(deviceSession, server)
	if err != nil {
		t.Fatalf("new tcp client: %v", err)
	}
	codec, err := session.NewCodecFromSession(deviceSession)
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		requestFrame, err := framing.NewReader(device, framing.DefaultMaxFrameSize).ReadFrame()
		if err != nil {
			done <- err
			return
		}
		requestPlaintext, err := codec.Decrypt(requestFrame)
		if err != nil {
			done <- err
			return
		}
		request, err := coap.Parse(requestPlaintext)
		if err != nil {
			done <- err
			return
		}
		if request.Path() != "u" || request.Code != coap.CodePut || len(request.Payload) != 0 {
			done <- errors.New("unexpected update done packet")
			return
		}
		done <- nil
	}()

	if err := tcpClient.CompleteFlash(context.Background(), &domain.FlashJob{ID: "job-1"}); err != nil {
		t.Fatalf("complete flash: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("packet: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for update done packet")
	}
}

func TestTCPClientHandlesChunkMissedPacket(t *testing.T) {
	var stream bytes.Buffer
	deviceSession := &session.Session{DeviceID: "device-1", SessionKey: []byte("0123456789abcdef")}
	tcpClient, err := tcp.NewClient(deviceSession, &stream)
	if err != nil {
		t.Fatalf("new tcp client: %v", err)
	}
	signals := &fakeFlashSignals{missed: make(chan []int, 1)}
	tcpClient.SetFlashSignalHandler(signals)

	handled, err := tcpClient.HandlePacketWithContext(context.Background(), &coap.Packet{
		Type:      coap.Confirmable,
		Code:      coap.CodeGet,
		MessageID: 77,
		Options: []coap.Option{
			{Number: coap.OptionURIPath, Value: []byte("c")},
		},
		Payload: []byte{0x00, 0x02, 0x00, 0x05},
	})
	if err != nil {
		t.Fatalf("handle packet: %v", err)
	}
	if !handled {
		t.Fatal("packet was not handled")
	}

	codec, err := session.NewCodecFromSession(deviceSession)
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}
	responseFrame, err := framing.NewReader(bytes.NewReader(stream.Bytes()), framing.DefaultMaxFrameSize).ReadFrame()
	if err != nil {
		t.Fatalf("read ack frame: %v", err)
	}
	responsePlaintext, err := codec.Decrypt(responseFrame)
	if err != nil {
		t.Fatalf("decrypt ack: %v", err)
	}
	response, err := coap.Parse(responsePlaintext)
	if err != nil {
		t.Fatalf("parse ack: %v", err)
	}
	if response.Type != coap.Acknowledgement || response.Code != coap.CodeEmpty || response.MessageID != 77 {
		t.Fatalf("response = %#v", response)
	}

	select {
	case indexes := <-signals.missed:
		if len(indexes) != 2 || indexes[0] != 2 || indexes[1] != 5 {
			t.Fatalf("indexes = %#v", indexes)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for missed chunk signal")
	}
}

func chunkQueryOptions(packet *coap.Packet) [][]byte {
	values := make([][]byte, 0)
	for _, option := range packet.Options {
		if option.Number == coap.OptionURIQuery {
			values = append(values, option.Value)
		}
	}
	return values
}

type fakeFlashSignals struct {
	missed chan []int
	abort  chan string
}

func (signals *fakeFlashSignals) RetryMissedFlashChunks(
	_            context.Context,
	_            string,
	chunkIndexes []int,
) (*domain.FlashJob, error) {
	signals.missed <- append([]int(nil), chunkIndexes...)
	return nil, nil
}

func (signals *fakeFlashSignals) AbortDeviceFlash(
	_       context.Context,
	_       string,
	message string,
) (*domain.FlashJob, error) {
	if signals.abort != nil {
		signals.abort <- message
	}
	return nil, nil
}

type fakeDeviceFirmwareUpdater struct {
	devices chan *domain.Device
}

func (updater *fakeDeviceFirmwareUpdater) CheckAndStartProductFirmwareUpdate(
	_      context.Context,
	device *domain.Device,
) (*domain.FlashJob, bool, error) {
	updater.devices <- device
	return nil, false, nil
}
