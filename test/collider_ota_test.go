// Package test verifies virtual OTA chunk delivery and binary reconstruction.
package test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"sparkserver/internal/auth"
	"sparkserver/internal/devices"
	"sparkserver/internal/events"
	"sparkserver/internal/firmware"
	"sparkserver/internal/httpapi"
	"sparkserver/internal/protocol/coap"
	protocoldevice "sparkserver/internal/protocol/device"
	protocolkeys "sparkserver/internal/protocol/keys"
	"sparkserver/internal/protocol/particle"
	"sparkserver/internal/protocol/tcp"
	filerepo "sparkserver/internal/repository/file"
)

func TestColliderVirtualDeviceReceivesAndReconstructsOTAFlash(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	keyManager := protocolkeys.NewManager(filepath.Join(dir, "keys"))
	if err := keyManager.EnsureServerKeyPair(); err != nil {
		t.Fatalf("ensure server key pair: %v", err)
	}

	authService := auth.NewService(
		filerepo.NewUserRepository(filepath.Join(dir, "users")),
		filerepo.NewAccessTokenRepository(filepath.Join(dir, "tokens")),
		24*time.Hour,
	)
	if _, err := authService.CreateUser(ctx, "__test__@testaccount.com", "password"); err != nil {
		t.Fatalf("create collider user: %v", err)
	}

	deviceService := devices.NewService(
		filerepo.NewDeviceRepository(filepath.Join(dir, "devices")),
		filerepo.NewDeviceClaimRepository(filepath.Join(dir, "deviceClaims")),
	)
	deviceService.SetAPITimeout(2 * time.Second)
	eventService := events.NewService(filerepo.NewEventRepository(filepath.Join(dir, "events")))
	firmwareService := firmware.NewService(
		filerepo.NewProductFirmwareRepository(filepath.Join(dir, "firmware", "metadata")),
		filepath.Join(dir, "firmware", "binaries"),
		filerepo.NewFlashJobRepository(filepath.Join(dir, "firmware", "flashJobs")),
	)
	firmwareService.SetFlashChunkSize(5)
	firmwareService.SetEventPublisher(eventService)

	protocolHandler := protocoldevice.NewHandler(eventService, deviceService)
	protocolHandler.SetFirmwareUpdater(firmwareService)
	tcpServer := tcp.New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	tcpServer.SetDeviceStatusUpdater(deviceService)
	tcpServer.SetFlashSignalHandler(firmwareService)
	deviceService.SetLiveClient(tcpServer)
	firmwareService.SetFlashTransport(tcpServer)

	httpHandler := httpapi.NewHandlerWithDeviceKeys(
		authService,
		deviceService,
		eventService,
		firmwareService,
		nil,
		nil,
		keyManager,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	token := loginColliderUser(t, httpHandler)
	virtualDevice := startLiveColliderDevice(ctx, t, httpHandler, token, keyManager, tcpServer, protocolHandler)
	t.Cleanup(func() {
		cancel()
		virtualDevice.close()
	})

	firmwareBytes := []byte{0x7f, 0x45, 0x4c, 0x46, 0x01, 0x02, 0x03, 0x08, 0x0d, 0x15, 0x22, 0x37}
	uploadColliderFirmware(t, httpHandler, token, "collider", firmwareBytes)

	responses := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := authedRequest(http.MethodPost, "/v1/devices/"+virtualDevice.identity.DeviceID+"/flash", `{"product_id":"collider"}`, token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		httpHandler.ServeHTTP(response, request)
		responses <- response
	}()

	beginRequest := virtualDevice.simulator.ReadRequest()
	if beginRequest.Code != coap.CodePost || beginRequest.Path() != particle.PathUpdate {
		t.Fatalf("begin request = %#v", beginRequest)
	}
	assertBeginFlashPayload(t, beginRequest.Payload, len(firmwareBytes), 5)
	virtualDevice.simulator.Respond(beginRequest, coap.CodeChanged, []byte{1})

	chunks := make(map[int][]byte)
	for len(chunks) < 3 {
		chunkRequest := virtualDevice.simulator.ReadRequest()
		index, payload := assertAndAckOTAChunk(t, virtualDevice, chunkRequest, 5)
		chunks[index] = append([]byte(nil), payload...)
	}

	completeRequest := virtualDevice.simulator.ReadRequest()
	if completeRequest.Code != coap.CodePut || completeRequest.Path() != particle.PathUpdate {
		t.Fatalf("complete request = %#v", completeRequest)
	}

	response := waitForHTTPResponse(t, responses)
	if response.Code != http.StatusAccepted {
		t.Fatalf("flash status = %d body = %s", response.Code, response.Body.String())
	}

	var job map[string]any
	if err := json.NewDecoder(response.Body).Decode(&job); err != nil {
		t.Fatalf("decode flash job: %v", err)
	}
	if job["device_id"] != virtualDevice.identity.DeviceID || job["product_id"] != "collider" || job["status"] != "completed" || job["progress"] != float64(100) || job["chunk_count"] != float64(3) {
		t.Fatalf("flash job = %#v", job)
	}

	reconstructed := reconstructChunks(chunks)
	if !bytes.Equal(reconstructed[:len(firmwareBytes)], firmwareBytes) {
		t.Fatalf("reconstructed bytes = %x want %x", reconstructed[:len(firmwareBytes)], firmwareBytes)
	}
	if tail := reconstructed[len(firmwareBytes):]; !bytes.Equal(tail, []byte{0, 0, 0}) {
		t.Fatalf("final chunk padding = %x", tail)
	}

	cancel()
	virtualDevice.close()
	virtualDevice.assertStopped(t)
}

func uploadColliderFirmware(
	t           *testing.T,
	httpHandler http.Handler,
	token       string,
	productID   string,
	payload     []byte,
) {
	t.Helper()

	request := authedRequest(http.MethodPost, "/v2/products/"+productID+"/firmwares?filename=collider.bin&current=true", string(payload), token)
	request.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	httpHandler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body = %s", response.Code, response.Body.String())
	}
}

func assertBeginFlashPayload(t *testing.T, payload []byte, size int, chunkSize int) {
	t.Helper()

	if len(payload) != 12 {
		t.Fatalf("begin payload length = %d payload=%x", len(payload), payload)
	}
	if payload[0] != 1 {
		t.Fatalf("begin protocol marker = %d", payload[0])
	}
	if binary.BigEndian.Uint16(payload[1:3]) != uint16(chunkSize) {
		t.Fatalf("begin chunk size = %d", binary.BigEndian.Uint16(payload[1:3]))
	}
	if binary.BigEndian.Uint32(payload[3:7]) != uint32(size) {
		t.Fatalf("begin size = %d", binary.BigEndian.Uint32(payload[3:7]))
	}
}

func assertAndAckOTAChunk(
	t             *testing.T,
	virtualDevice *liveColliderDevice,
	request       *coap.Packet,
	chunkSize     int,
) (int, []byte) {
	t.Helper()

	if request.Code != coap.CodePost || request.Path() != particle.PathChunkShort {
		t.Fatalf("chunk request = %#v", request)
	}
	if len(request.Payload) != chunkSize {
		t.Fatalf("chunk payload length = %d payload=%x", len(request.Payload), request.Payload)
	}

	checksum, index := chunkMetadata(t, request)
	actualChecksum := crc32.ChecksumIEEE(request.Payload)
	if checksum != actualChecksum {
		t.Fatalf("chunk checksum = %08x want %08x", checksum, actualChecksum)
	}

	ack := make([]byte, 4)
	binary.BigEndian.PutUint32(ack, checksum)
	virtualDevice.simulator.Respond(request, coap.CodeChanged, ack)
	return index, request.Payload
}

func chunkMetadata(t *testing.T, request *coap.Packet) (uint32, int) {
	t.Helper()

	var checksum uint32
	index := -1
	for _, option := range request.Options {
		if option.Number != coap.OptionURIQuery {
			continue
		}
		switch len(option.Value) {
		case 4:
			checksum = binary.BigEndian.Uint32(option.Value)
		case 2:
			index = int(binary.BigEndian.Uint16(option.Value))
		}
	}
	if checksum == 0 || index < 0 {
		t.Fatalf("chunk query options = %#v", request.Options)
	}
	return checksum, index
}

func reconstructChunks(chunks map[int][]byte) []byte {
	indexes := make([]int, 0, len(chunks))
	for index := range chunks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	var reconstructed []byte
	for _, index := range indexes {
		reconstructed = append(reconstructed, chunks[index]...)
	}
	return reconstructed
}
