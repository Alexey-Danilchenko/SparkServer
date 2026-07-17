// Package test verifies event publishing and SSE behavior.
package test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sparkserver/internal/auth"
	"sparkserver/internal/domain"
	"sparkserver/internal/events"
	"sparkserver/internal/httpapi"
	filerepo "sparkserver/internal/repository/file"
)

func TestPublishEventRoute(t *testing.T) {
	handler, token := newAuthenticatedEventHandler(t)

	request := authedRequest(http.MethodPost, "/v1/devices/events", `{"name":"brew.started","data":"hot"}`, token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("publish status = %d body = %s", response.Code, response.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode publish response: %v", err)
	}
	if body["ok"] != true || body["name"] != "brew.started" {
		t.Fatalf("publish response = %#v", body)
	}
}

func TestPingRoutesEchoPayload(t *testing.T) {
	handler, _ := newAuthenticatedEventHandler(t)

	for _, path := range []string{"/v1/ping", "/v2/ping"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"client":"sparkctl"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d body = %s", path, response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s ping: %v", path, err)
		}
		if body["client"] != "sparkctl" || body["serverPayload"] == nil {
			t.Fatalf("%s ping body = %#v", path, body)
		}
	}
}

func TestSSEReceivesPublishedEvents(t *testing.T) {
	handler, token := newAuthenticatedEventHandler(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streamRequest := httptest.NewRequest(http.MethodGet, "/v1/events/brew.", nil).WithContext(ctx)
	streamRequest.Header.Set("Authorization", "Bearer "+token)
	streamResponse := httptest.NewRecorder()
	done := make(chan struct{})

	go func() {
		handler.ServeHTTP(streamResponse, streamRequest)
		close(done)
	}()

	time.Sleep(25 * time.Millisecond)

	publishRequest := authedRequest(http.MethodPost, "/v1/devices/events", `{"name":"brew.started","data":"hot"}`, token)
	publishRequest.Header.Set("Content-Type", "application/json")
	publishResponse := httptest.NewRecorder()
	handler.ServeHTTP(publishResponse, publishRequest)

	if publishResponse.Code != http.StatusOK {
		t.Fatalf("publish status = %d body = %s", publishResponse.Code, publishResponse.Body.String())
	}

	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(streamResponse.Body.String(), "event: brew.started") {
			break
		}

		select {
		case <-deadline:
			t.Fatalf("timed out waiting for SSE event, body = %s", streamResponse.Body.String())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream handler did not stop after cancel")
	}
}

func TestSSEPrefixFilter(t *testing.T) {
	eventService := events.NewService(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := eventService.Subscribe(ctx, events.Filter{Prefix: "brew."})
	if _, err := eventService.Publish(context.Background(), eventFromTest("device.online")); err != nil {
		t.Fatalf("publish unrelated event: %v", err)
	}
	if _, err := eventService.Publish(context.Background(), eventFromTest("brew.done")); err != nil {
		t.Fatalf("publish matching event: %v", err)
	}

	select {
	case event := <-events:
		if event.Name != "brew.done" {
			t.Fatalf("event name = %q", event.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for matching event")
	}
}

func newAuthenticatedEventHandler(t *testing.T) (http.Handler, string) {
	t.Helper()

	dir := t.TempDir()
	authService := auth.NewService(
		filerepo.NewUserRepository(filepath.Join(dir, "users")),
		filerepo.NewAccessTokenRepository(filepath.Join(dir, "tokens")),
		24*time.Hour,
	)
	eventService := events.NewService(filerepo.NewEventRepository(filepath.Join(dir, "events")))

	if err := authService.EnsureDefaultAdmin(context.Background(), "__admin__", "adminPassword"); err != nil {
		t.Fatalf("ensure default admin: %v", err)
	}

	handler := httpapi.NewHandler(authService, nil, eventService, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

func eventFromTest(name string) *domain.Event {
	return &domain.Event{Name: name}
}
