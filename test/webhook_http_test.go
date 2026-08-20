// Package test verifies webhook HTTP CRUD and delivery behavior.
package test

import (
	"context"
	"encoding/json"
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
	"sparkserver/internal/firmware"
	"sparkserver/internal/httpapi"
	jsonfile "sparkserver/internal/jsonfile"
	"sparkserver/internal/products"
	"sparkserver/internal/webhooks"
)

func TestWebhookRoutesAndDelivery(t *testing.T) {
	delivered := make(chan map[string]any, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.Header.Get("X-Spark-Test") != "yes" {
			t.Errorf("header X-Spark-Test = %q", r.Header.Get("X-Spark-Test"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
		}
		delivered <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer receiver.Close()

	handler, token := newAuthenticatedWebhookHandler(t)

	create := authedRequest(http.MethodPost, "/v1/webhooks", `{"event":"brew.started","url":"`+receiver.URL+`","headers":{"X-Spark-Test":"yes"}}`, token)
	create.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createResponse.Code, createResponse.Body.String())
	}

	var webhook map[string]any
	if err := json.NewDecoder(createResponse.Body).Decode(&webhook); err != nil {
		t.Fatalf("decode webhook: %v", err)
	}
	if webhook["event"] != "brew.started" || webhook["url"] != receiver.URL || webhook["method"] != http.MethodPost {
		t.Fatalf("webhook = %#v", webhook)
	}

	list := authedRequest(http.MethodGet, "/v1/webhooks", "", token)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listResponse.Code, listResponse.Body.String())
	}
	var webhooks []map[string]any
	if err := json.NewDecoder(listResponse.Body).Decode(&webhooks); err != nil {
		t.Fatalf("decode webhooks: %v", err)
	}
	if len(webhooks) != 1 || webhooks[0]["id"] != webhook["id"] {
		t.Fatalf("webhooks = %#v", webhooks)
	}

	update := authedRequest(http.MethodPut, "/v1/webhooks/"+webhook["id"].(string), `{"body":"{\"event\":\"{{event}}\",\"data\":\"{{data}}\",\"coreid\":\"{{coreid}}\"}"}`, token)
	update.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("update status = %d body = %s", updateResponse.Code, updateResponse.Body.String())
	}

	publish := authedRequest(http.MethodPost, "/v1/devices/events", `{"name":"brew.started","data":"hot","coreid":"device-1"}`, token)
	publish.Header.Set("Content-Type", "application/json")
	publishResponse := httptest.NewRecorder()
	handler.ServeHTTP(publishResponse, publish)
	if publishResponse.Code != http.StatusOK {
		t.Fatalf("publish status = %d body = %s", publishResponse.Code, publishResponse.Body.String())
	}

	select {
	case payload := <-delivered:
		if payload["event"] != "brew.started" || payload["data"] != "hot" || payload["coreid"] != "device-1" {
			t.Fatalf("payload = %#v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook delivery")
	}

	remove := authedRequest(http.MethodDelete, "/v1/webhooks/"+webhook["id"].(string), "", token)
	removeResponse := httptest.NewRecorder()
	handler.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body = %s", removeResponse.Code, removeResponse.Body.String())
	}
}

func TestWebhookDeliveryFailureBackoff(t *testing.T) {
	attempts := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer receiver.Close()

	handler, token := newAuthenticatedWebhookHandler(t)

	create := authedRequest(http.MethodPost, "/v1/webhooks", `{"event":"brew.failed","url":"`+receiver.URL+`"}`, token)
	create.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createResponse.Code, createResponse.Body.String())
	}
	var webhook map[string]any
	if err := json.NewDecoder(createResponse.Body).Decode(&webhook); err != nil {
		t.Fatalf("decode webhook: %v", err)
	}

	for range 2 {
		publish := authedRequest(http.MethodPost, "/v1/devices/events", `{"name":"brew.failed","data":"bad"}`, token)
		publish.Header.Set("Content-Type", "application/json")
		publishResponse := httptest.NewRecorder()
		handler.ServeHTTP(publishResponse, publish)
		if publishResponse.Code != http.StatusOK {
			t.Fatalf("publish status = %d body = %s", publishResponse.Code, publishResponse.Body.String())
		}
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d", attempts)
	}

	get := authedRequest(http.MethodGet, "/v1/webhooks/"+webhook["id"].(string), "", token)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", getResponse.Code, getResponse.Body.String())
	}
	var updated map[string]any
	if err := json.NewDecoder(getResponse.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated webhook: %v", err)
	}
	if updated["fail_count"] != float64(1) || updated["last_status"] != float64(500) || updated["next_attempt_at"] == nil {
		t.Fatalf("updated webhook = %#v", updated)
	}
}

func newAuthenticatedWebhookHandler(t *testing.T) (http.Handler, string) {
	t.Helper()

	dir := t.TempDir()
	authService := auth.NewService(
		jsonfile.NewUserRepository(filepath.Join(dir, "users")),
		jsonfile.NewAccessTokenRepository(filepath.Join(dir, "tokens")),
		24*time.Hour,
	)
	deviceRepository := jsonfile.NewDeviceRepository(filepath.Join(dir, "devices"))
	deviceService := devices.NewService(
		deviceRepository,
		jsonfile.NewDeviceClaimRepository(filepath.Join(dir, "deviceClaims")),
	)
	eventService := events.NewService(jsonfile.NewEventRepository(filepath.Join(dir, "events")))
	webhookService := webhooks.NewService(jsonfile.NewWebhookRepository(filepath.Join(dir, "webhooks")))
	eventService.AddSink(webhookService)
	firmwareService := firmware.NewService(
		jsonfile.NewProductFirmwareRepository(filepath.Join(dir, "firmware", "metadata")),
		filepath.Join(dir, "firmware", "binaries"),
		jsonfile.NewFlashJobRepository(filepath.Join(dir, "firmware", "flashJobs")),
	)
	productService := products.NewService(
		jsonfile.NewProductRepository(filepath.Join(dir, "products")),
		jsonfile.NewProductDeviceRepository(filepath.Join(dir, "products", "devices")),
		deviceRepository,
	)

	if err := authService.EnsureDefaultAdmin(context.Background(), "__admin__", "adminPassword"); err != nil {
		t.Fatalf("ensure default admin: %v", err)
	}

	handler := httpapi.NewHandler(
		httpapi.Dependencies{
			Auth: authService, Devices: deviceService, Events: eventService,
			Firmware: firmwareService, Products: productService, Webhooks: webhookService,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	response := postForm(t, handler, "/oauth/token", "grant_type=password&username=__admin__&password=adminPassword")
	if response.Code != http.StatusOK {
		t.Fatalf("token status = %d body = %s", response.Code, response.Body.String())
	}

	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	return handler, body.AccessToken
}
