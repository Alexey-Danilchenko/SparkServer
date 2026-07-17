// Package test verifies collider variable/function flows over live TCP sessions.
package test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"sparkserver/internal/devices"
	"sparkserver/internal/events"
	"sparkserver/internal/protocol/coap"
	protocoldevice "sparkserver/internal/protocol/device"
	"sparkserver/internal/protocol/handshake"
	protocolkeys "sparkserver/internal/protocol/keys"
	"sparkserver/internal/protocol/particle"
	"sparkserver/internal/protocol/tcp"
	filerepo "sparkserver/internal/repository/file"
	"sparkserver/test/collider"
)

func TestColliderDeviceCompletesEncryptedTCPFlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	keyManager := protocolkeys.NewManager(filepath.Join(dir, "keys"))
	if err := keyManager.EnsureServerKeyPair(); err != nil {
		t.Fatalf("ensure server key pair: %v", err)
	}

	deviceService := devices.NewService(
		filerepo.NewDeviceRepository(filepath.Join(dir, "devices")),
		filerepo.NewDeviceClaimRepository(filepath.Join(dir, "deviceClaims")),
	)
	deviceService.SetAPITimeout(2 * time.Second)
	eventService := events.NewService(nil)
	protocolHandler := protocoldevice.NewHandler(eventService, deviceService)
	tcpServer := tcp.New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	tcpServer.SetDeviceStatusUpdater(deviceService)
	deviceService.SetLiveClient(tcpServer)

	deviceConn, serverConn := net.Pipe()
	simulator := collider.New(t, deviceConn, "device-1")
	t.Cleanup(func() {
		cancel()
		simulator.Close()
		_ = serverConn.Close()
	})

	serverErrors := serveColliderConnection(ctx, t, serverConn, keyManager, tcpServer, protocolHandler)
	simulator.Handshake(keyManager)

	collider.WaitUntil(t, func() bool {
		_, ok := tcpServer.Registry().Get("device-1")
		return ok
	})

	descriptionResponse := simulator.Describe(`{"variables":{"temperature":"double"},"functions":["brew"],"product_id":"product-1","product_firmware_version":1}`)
	if descriptionResponse.Code != coap.CodeChanged {
		t.Fatalf("describe response = %#v", descriptionResponse)
	}

	storedDevice, err := deviceService.Get(ctx, "", "device-1")
	if err != nil {
		t.Fatalf("get described device: %v", err)
	}
	if !storedDevice.Connected {
		t.Fatal("device was not marked connected")
	}
	if storedDevice.Variables["temperature"] != "double" {
		t.Fatalf("variables = %#v", storedDevice.Variables)
	}
	if len(storedDevice.Functions) != 1 || storedDevice.Functions[0] != "brew" {
		t.Fatalf("functions = %#v", storedDevice.Functions)
	}
	if storedDevice.ProductID != "product-1" {
		t.Fatalf("product id = %q", storedDevice.ProductID)
	}

	variableResults := make(chan variableResult, 1)
	go func() {
		value, err := deviceService.GetVariable(ctx, "", "device-1", "temperature")
		variableResults <- variableResult{value: value, err: err}
	}()

	variableRequest := simulator.ReadRequest()
	if variableRequest.Code != coap.CodeGet || variableRequest.Path() != "variable/temperature" {
		t.Fatalf("variable request = %#v", variableRequest)
	}
	simulator.Respond(variableRequest, coap.CodeContent, []byte(`{"result":21.5}`))
	select {
	case result := <-variableResults:
		if result.err != nil {
			t.Fatalf("get variable: %v", result.err)
		}
		if result.value != "21.5" {
			t.Fatalf("variable value = %q", result.value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for variable result")
	}

	functionResults := make(chan functionResult, 1)
	go func() {
		value, err := deviceService.CallFunction(ctx, "", "device-1", "brew", "start")
		functionResults <- functionResult{value: value, err: err}
	}()

	functionRequest := simulator.ReadRequest()
	if functionRequest.Code != coap.CodePost || functionRequest.Path() != "function/brew" {
		t.Fatalf("function request = %#v", functionRequest)
	}
	if functionRequest.QueryValues().Get(particle.QueryArgument) != "start" {
		t.Fatalf("function query = %#v", functionRequest.QueryValues())
	}
	if string(functionRequest.Payload) != "start" {
		t.Fatalf("function payload = %q", string(functionRequest.Payload))
	}
	simulator.Respond(functionRequest, coap.CodeChanged, []byte(`{"return_value":7}`))
	select {
	case result := <-functionResults:
		if result.err != nil {
			t.Fatalf("call function: %v", result.err)
		}
		if result.value != 7 {
			t.Fatalf("function value = %d", result.value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for function result")
	}

	receivedEvents := eventService.Subscribe(ctx, events.Filter{DeviceID: "device-1"})
	publishResponse := simulator.Publish("brew.started", "hot")
	if publishResponse.Code != coap.CodeChanged {
		t.Fatalf("publish response = %#v", publishResponse)
	}
	select {
	case event := <-receivedEvents:
		if event.Name != "brew.started" || event.Data != "hot" || event.DeviceID != "device-1" {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published event")
	}

	cancel()
	simulator.Close()
	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("serve collider connection: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server session to stop")
	}
}

type variableResult struct {
	value string
	err   error
}

type functionResult struct {
	value int
	err   error
}

func serveColliderConnection(
	ctx        context.Context,
	t          *testing.T,
	conn       net.Conn,
	keyManager *protocolkeys.Manager,
	tcpServer  *tcp.Server,
	handler    *protocoldevice.Handler,
) <-chan error {
	t.Helper()

	done := make(chan error, 1)
	go func() {
		defer close(done)
		defer conn.Close()

		deviceSession, err := handshake.NewHandshaker(keyManager).Handshake(ctx, conn)
		if err != nil {
			done <- err
			return
		}

		tcpClient, err := tcp.NewClient(deviceSession, conn)
		if err != nil {
			done <- err
			return
		}

		cleanup := tcpServer.RegisterDeviceClient(ctx, deviceSession.DeviceID, conn, tcpClient)
		err = tcp.ServeSession(ctx, conn, deviceSession, tcpClient, handler, func(deviceID string) {
			tcpServer.Registry().Touch(deviceID)
		})
		cleanup()
		done <- err
	}()
	return done
}
