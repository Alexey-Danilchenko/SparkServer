// Package test stress-tests mixed collider flows with device churn.
package test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	"sparkserver/internal/protocol/tcp"
	"sparkserver/internal/webhooks"
)

func TestColliderChaosMonkeyThrashesVirtualDevices(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	deliveries := make(chan map[string]any, 16)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
		}
		deliveries <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer receiver.Close()

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
	eventService := events.NewService(jsonfile.NewEventRepository(filepath.Join(dir, "events")))
	webhookService := webhooks.NewService(jsonfile.NewWebhookRepository(filepath.Join(dir, "webhooks")))
	eventService.AddSink(webhookService)

	protocolHandler := protocoldevice.NewHandler(eventService, deviceService)
	tcpServer := tcp.New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	tcpServer.SetDeviceStatusUpdater(deviceService)
	deviceService.SetLiveClient(tcpServer)

	httpHandler := httpapi.NewHandler(
		httpapi.Dependencies{
			Auth: authService, Devices: deviceService, Events: eventService,
			Webhooks: webhookService, DeviceKeys: keyManager,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	token := loginColliderUser(t, httpHandler)
	createColliderWebhook(t, httpHandler, token, receiver.URL)

	active := make([]*liveColliderDevice, 0, 6)
	addDevice := func() {
		active = append(active, startLiveColliderDevice(ctx, t, httpHandler, token, keyManager, tcpServer, protocolHandler))
	}
	removeDevice := func(index int) {
		removed := active[index]
		active = append(active[:index], active[index+1:]...)
		removed.close()
		removed.assertStopped(t)
		if _, ok := tcpServer.Registry().Get(removed.identity.DeviceID); ok {
			t.Fatalf("removed device %s still registered", removed.identity.DeviceID)
		}
	}
	t.Cleanup(func() {
		cancel()
		for _, virtualDevice := range active {
			virtualDevice.close()
		}
	})

	for range 3 {
		addDevice()
	}

	random := rand.New(rand.NewSource(20260717))
	functionCalls := 0
	variableCalls := 0
	webhookCalls := 0
	adds := len(active)
	removes := 0

	for iteration := range 128 {
		if len(active) == 0 {
			addDevice()
			adds++
			continue
		}

		deviceIndex := random.Intn(len(active))
		virtualDevice := active[deviceIndex]
		switch random.Intn(5) {
		case 0:
			if len(active) < 6 {
				addDevice()
				adds++
				continue
			}
			callColliderVariable(t, httpHandler, token, virtualDevice, 30000+iteration)
			variableCalls++
		case 1:
			if len(active) > 1 {
				removeDevice(deviceIndex)
				removes++
				continue
			}
			callColliderFunction(t, httpHandler, token, virtualDevice, iteration, 40000+iteration)
			functionCalls++
		case 2:
			callColliderVariable(t, httpHandler, token, virtualDevice, 30000+iteration)
			variableCalls++
		case 3:
			callColliderFunction(t, httpHandler, token, virtualDevice, iteration, 40000+iteration)
			functionCalls++
		default:
			payload := fmt.Sprintf(`{"payload":"chaos-%d"}`, iteration)
			response := virtualDevice.simulator.Publish("test-webhook", payload)
			if response.Code != coap.CodeChanged {
				t.Fatalf("publish response = %#v", response)
			}
			delivery := waitForWebhookDelivery(t, deliveries)
			if delivery["event"] != "test-webhook" || delivery["coreid"] != virtualDevice.identity.DeviceID || delivery["data"] != payload {
				t.Fatalf("delivery = %#v", delivery)
			}
			webhookCalls++
		}
	}

	if adds < 4 || removes == 0 || functionCalls == 0 || variableCalls == 0 || webhookCalls == 0 {
		t.Fatalf("chaos coverage adds=%d removes=%d functions=%d variables=%d webhooks=%d", adds, removes, functionCalls, variableCalls, webhookCalls)
	}

	cancel()
	for len(active) > 0 {
		removeDevice(len(active) - 1)
	}
}
