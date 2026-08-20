// Package test bridges HTTP commands to virtual live devices.
package test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sparkserver/internal/auth"
	"sparkserver/internal/devices"
	"sparkserver/internal/events"
	"sparkserver/internal/httpapi"
	jsonfile "sparkserver/internal/jsonfile"
	"sparkserver/internal/protocol/coap"
	protocoldevice "sparkserver/internal/protocol/device"
	protocolkeys "sparkserver/internal/protocol/keys"
	"sparkserver/internal/protocol/particle"
	"sparkserver/internal/protocol/tcp"
	"sparkserver/test/collider"
)

func TestColliderRandomHTTPFunctionAndVariableCallsReachVirtualDevices(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	keyManager := protocolkeys.NewManager(filepath.Join(dir, "keys"))
	if err := keyManager.EnsureServerKeyPair(); err != nil {
		t.Fatalf("ensure server key pair: %v", err)
	}

	authService := auth.NewService(
		jsonfile.NewUserRepository(filepath.Join(dir, "users")),
		jsonfile.NewAccessTokenRepository(filepath.Join(dir, "tokens")),
		24*time.Hour,
	)
	if _, err := authService.CreateUser(ctx, "__test__@testaccount.com", "password"); err != nil {
		t.Fatalf("create collider user: %v", err)
	}

	deviceService := devices.NewService(
		jsonfile.NewDeviceRepository(filepath.Join(dir, "devices")),
		jsonfile.NewDeviceClaimRepository(filepath.Join(dir, "deviceClaims")),
		devices.WithAPITimeout(2*time.Second),
	)
	eventService := events.NewService(nil)
	protocolHandler := protocoldevice.NewHandler(eventService, deviceService)
	tcpServer := tcp.New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	tcpServer.SetDeviceStatusUpdater(deviceService)
	deviceService.SetLiveClient(tcpServer)

	httpHandler := httpapi.NewHandler(
		httpapi.Dependencies{
			Auth: authService, Devices: deviceService, Events: eventService, DeviceKeys: keyManager,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	token := loginColliderUser(t, httpHandler)

	virtualDevices := make([]*liveColliderDevice, 0, 4)
	for range 4 {
		virtualDevice := startLiveColliderDevice(ctx, t, httpHandler, token, keyManager, tcpServer, protocolHandler)
		virtualDevices = append(virtualDevices, virtualDevice)
	}
	t.Cleanup(func() {
		cancel()
		for _, virtualDevice := range virtualDevices {
			virtualDevice.close()
		}
	})

	random := rand.New(rand.NewSource(42))
	for iteration := range 16 {
		virtualDevice := virtualDevices[random.Intn(len(virtualDevices))]
		if random.Intn(2) == 0 {
			callColliderVariable(t, httpHandler, token, virtualDevice, 10000+iteration)
			continue
		}
		callColliderFunction(t, httpHandler, token, virtualDevice, iteration, 20000+iteration)
	}

	cancel()
	for _, virtualDevice := range virtualDevices {
		virtualDevice.close()
		virtualDevice.assertStopped(t)
	}
}

type liveColliderDevice struct {
	identity   collider.Identity
	simulator  *collider.Device
	serverConn net.Conn
	errors     <-chan error
}

func startLiveColliderDevice(
	ctx context.Context,
	t *testing.T,
	httpHandler http.Handler,
	token string,
	keyManager *protocolkeys.Manager,
	tcpServer *tcp.Server,
	protocolHandler *protocoldevice.Handler,
) *liveColliderDevice {
	t.Helper()

	identity := collider.NewIdentity(t)
	registerColliderPublicKey(t, httpHandler, token, identity)

	deviceConn, serverConn := net.Pipe()
	simulator := collider.New(t, deviceConn, identity.DeviceID)
	errors := serveColliderConnection(ctx, t, serverConn, keyManager, tcpServer, protocolHandler)
	simulator.Handshake(keyManager)

	collider.WaitUntil(t, func() bool {
		_, ok := tcpServer.Registry().Get(identity.DeviceID)
		return ok
	})

	descriptionResponse := simulator.Describe(`{"f":["testfn"],"v":{"testVar":"INT"},"product_id":"collider","product_firmware_version":1}`)
	if descriptionResponse.Code != coap.CodeChanged {
		t.Fatalf("describe response = %#v", descriptionResponse)
	}

	return &liveColliderDevice{
		identity:   identity,
		simulator:  simulator,
		serverConn: serverConn,
		errors:     errors,
	}
}

func registerColliderPublicKey(
	t *testing.T,
	httpHandler http.Handler,
	token string,
	identity collider.Identity,
) {
	t.Helper()

	body, err := json.Marshal(map[string]string{
		"deviceID":  identity.DeviceID,
		"publicKey": identity.PublicKeyPEM,
	})
	if err != nil {
		t.Fatalf("marshal public key request: %v", err)
	}

	request := authedRequest(http.MethodPost, "/v1/provisioning/"+identity.DeviceID, string(body), token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpHandler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("register public key status = %d body = %s", response.Code, response.Body.String())
	}
}

func loginColliderUser(t *testing.T, httpHandler http.Handler) string {
	t.Helper()

	response := postForm(t, httpHandler, "/oauth/token", "grant_type=password&username=__test__@testaccount.com&password=password")
	if response.Code != http.StatusOK {
		t.Fatalf("token status = %d body = %s", response.Code, response.Body.String())
	}

	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if body.AccessToken == "" {
		t.Fatal("missing access token")
	}
	return body.AccessToken
}

func callColliderVariable(
	t *testing.T,
	httpHandler http.Handler,
	token string,
	virtualDevice *liveColliderDevice,
	result int,
) {
	t.Helper()

	responses := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := authedRequest(http.MethodGet, "/v1/devices/"+virtualDevice.identity.DeviceID+"/testVar", "", token)
		response := httptest.NewRecorder()
		httpHandler.ServeHTTP(response, request)
		responses <- response
	}()

	deviceRequest := virtualDevice.simulator.ReadRequest()
	if deviceRequest.Code != coap.CodeGet || deviceRequest.Path() != "variable/testVar" {
		t.Fatalf("variable request = %#v", deviceRequest)
	}
	virtualDevice.simulator.Respond(deviceRequest, coap.CodeContent, intPayload(result))

	response := waitForHTTPResponse(t, responses)
	if response.Code != http.StatusOK {
		t.Fatalf("variable status = %d body = %s", response.Code, response.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode variable response: %v", err)
	}
	if body["name"] != "testVar" || body["result"] != fmt.Sprint(result) {
		t.Fatalf("variable response = %#v", body)
	}
}

func callColliderFunction(
	t *testing.T,
	httpHandler http.Handler,
	token string,
	virtualDevice *liveColliderDevice,
	iteration int,
	result int,
) {
	t.Helper()

	argument := fmt.Sprintf("collider-%d", iteration)
	responses := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := authedRequest(http.MethodPost, "/v1/devices/"+virtualDevice.identity.DeviceID+"/testfn", "arg="+url.QueryEscape(argument), token)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		httpHandler.ServeHTTP(response, request)
		responses <- response
	}()

	deviceRequest := virtualDevice.simulator.ReadRequest()
	if deviceRequest.Code != coap.CodePost || deviceRequest.Path() != "function/testfn" {
		t.Fatalf("function request = %#v", deviceRequest)
	}
	if deviceRequest.QueryValues().Get(particle.QueryArgument) != argument {
		t.Fatalf("function query = %#v", deviceRequest.QueryValues())
	}
	if string(deviceRequest.Payload) != argument {
		t.Fatalf("function payload = %q", string(deviceRequest.Payload))
	}
	virtualDevice.simulator.Respond(deviceRequest, coap.CodeChanged, intPayload(result))

	response := waitForHTTPResponse(t, responses)
	if response.Code != http.StatusOK {
		t.Fatalf("function status = %d body = %s", response.Code, response.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode function response: %v", err)
	}
	if body["name"] != "testfn" || body["return_value"] != float64(result) {
		t.Fatalf("function response = %#v", body)
	}
}

func waitForHTTPResponse(
	t *testing.T,
	responses <-chan *httptest.ResponseRecorder,
) *httptest.ResponseRecorder {
	t.Helper()

	select {
	case response := <-responses:
		return response
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for HTTP response")
		return nil
	}
}

func intPayload(value int) []byte {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(value))
	return payload
}

func (device *liveColliderDevice) close() {
	device.simulator.Close()
	_ = device.serverConn.Close()
}

func (device *liveColliderDevice) assertStopped(t *testing.T) {
	t.Helper()

	select {
	case err := <-device.errors:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Fatalf("serve collider connection: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server session to stop")
	}
}
