// Package test verifies collider-originated events triggering webhooks.
package test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

func TestColliderVirtualDevicesPublishWebhookEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	deliveries := make(chan map[string]any, 8)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.Header.Get("X-Collider-Test") != "yes" {
			t.Errorf("header X-Collider-Test = %q", r.Header.Get("X-Collider-Test"))
		}

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

	virtualDevices := make([]*liveColliderDevice, 0, 3)
	for index := 0; index < 3; index++ {
		virtualDevice := startLiveColliderDevice(ctx, t, httpHandler, token, keyManager, tcpServer, protocolHandler)
		virtualDevices = append(virtualDevices, virtualDevice)
	}
	t.Cleanup(func() {
		cancel()
		for _, virtualDevice := range virtualDevices {
			virtualDevice.close()
		}
	})

	for index, virtualDevice := range virtualDevices {
		payload := fmt.Sprintf(`{"payload":"collider-%d"}`, index)
		response := virtualDevice.simulator.Publish("test-webhook", payload)
		if response.Code != coap.CodeChanged {
			t.Fatalf("publish response = %#v", response)
		}

		delivery := waitForWebhookDelivery(t, deliveries)
		if delivery["event"] != "test-webhook" || delivery["name"] != "test-webhook" || delivery["coreid"] != virtualDevice.identity.DeviceID {
			t.Fatalf("delivery = %#v", delivery)
		}
		if delivery["data"] != payload {
			t.Fatalf("delivery data = %#v", delivery["data"])
		}
	}

	cancel()
	for _, virtualDevice := range virtualDevices {
		virtualDevice.close()
		virtualDevice.assertStopped(t)
	}
}

func createColliderWebhook(
	t *testing.T,
	httpHandler http.Handler,
	token string,
	receiverURL string,
) {
	t.Helper()

	request := authedRequest(http.MethodPost, "/v1/webhooks", `{"event":"test-webhook","url":"`+receiverURL+`","headers":{"X-Collider-Test":"yes"}}`, token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpHandler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create webhook status = %d body = %s", response.Code, response.Body.String())
	}
}

func waitForWebhookDelivery(t *testing.T, deliveries <-chan map[string]any) map[string]any {
	t.Helper()

	select {
	case delivery := <-deliveries:
		return delivery
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook delivery")
		return nil
	}
}
