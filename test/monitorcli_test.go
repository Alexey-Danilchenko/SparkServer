// Package test verifies sparkctl command behavior.
package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sparkserver/internal/monitorcli"
)

func TestMonitorCLIListsDevicesWithAutomaticLogin(t *testing.T) {
	server := newMonitorCLITestServer(t)
	defer server.Close()

	var out bytes.Buffer
	err := monitorcli.Run(context.Background(), []string{
		"-base", server.URL,
		"-username", "__admin__",
		"-password", "adminPassword",
		"devices",
	}, monitorcli.Options{Out: &out, Err: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("run devices: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "device-1") || !strings.Contains(output, "temperature") || !strings.Contains(output, "brew") {
		t.Fatalf("devices output = %q", output)
	}
}

func TestMonitorCLIReadsVariableAndCallsFunction(t *testing.T) {
	server := newMonitorCLITestServer(t)
	defer server.Close()

	var variableOut bytes.Buffer
	if err := monitorcli.Run(context.Background(), []string{
		"-base", server.URL,
		"-token", "token-123",
		"variable", "device-1", "temperature",
	}, monitorcli.Options{Out: &variableOut, Err: &bytes.Buffer{}}); err != nil {
		t.Fatalf("run variable: %v", err)
	}
	if strings.TrimSpace(variableOut.String()) != "temperature=21.5" {
		t.Fatalf("variable output = %q", variableOut.String())
	}

	var functionOut bytes.Buffer
	if err := monitorcli.Run(context.Background(), []string{
		"-base", server.URL,
		"-token", "token-123",
		"function", "device-1", "brew", "start",
	}, monitorcli.Options{Out: &functionOut, Err: &bytes.Buffer{}}); err != nil {
		t.Fatalf("run function: %v", err)
	}
	if strings.TrimSpace(functionOut.String()) != "brew returned 7" {
		t.Fatalf("function output = %q", functionOut.String())
	}
}

func TestMonitorCLIStreamsEventsAsJSON(t *testing.T) {
	server := newMonitorCLITestServer(t)
	defer server.Close()

	var out bytes.Buffer
	err := monitorcli.Run(context.Background(), []string{
		"-base", server.URL,
		"-token", "token-123",
		"-json",
		"events", "-device", "device-1", "-prefix", "brew",
	}, monitorcli.Options{Out: &out, Err: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("run events: %v", err)
	}

	var event monitorcli.Event
	if err := json.Unmarshal(out.Bytes(), &event); err != nil {
		t.Fatalf("decode event output %q: %v", out.String(), err)
	}
	if event.Name != "brew.started" || event.CoreID != "device-1" || event.Data != "hot" {
		t.Fatalf("event = %#v", event)
	}
}

func newMonitorCLITestServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			if r.Method != http.MethodPost {
				t.Fatalf("login method = %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse login form: %v", err)
			}
			if r.Form.Get("username") != "__admin__" || r.Form.Get("password") != "adminPassword" {
				t.Fatalf("login form = %#v", r.Form)
			}
			writeMonitorJSON(t, w, map[string]any{"access_token": "token-123", "token_type": "bearer"})
			return
		}

		if r.Header.Get("Authorization") != "Bearer token-123" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/devices":
			writeMonitorJSON(t, w, []map[string]any{{
				"id":         "device-1",
				"name":       "Kettle",
				"online":     true,
				"connected":  true,
				"product_id": "brewery",
				"variables":  map[string]string{"temperature": "DOUBLE"},
				"functions":  []string{"brew"},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/devices/device-1/temperature":
			writeMonitorJSON(t, w, map[string]any{"cmd": "VarReturn", "name": "temperature", "result": "21.5"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices/device-1/brew":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse function form: %v", err)
			}
			if r.Form.Get("arg") != "start" {
				t.Fatalf("function arg = %q", r.Form.Get("arg"))
			}
			writeMonitorJSON(t, w, map[string]any{"id": "device-1", "name": "brew", "connected": true, "return_value": 7})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/devices/device-1/events/brew":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: brew.started\n")
			fmt.Fprint(w, `data: {"name":"brew.started","data":"hot","coreid":"device-1","published_at":"now"}`+"\n\n")
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
}

func writeMonitorJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("write JSON: %v", err)
	}
}
