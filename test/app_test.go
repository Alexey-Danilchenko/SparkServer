// Package test verifies application wiring and end-to-end server lifecycle behavior.
package test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sparkserver/internal/app"
	"sparkserver/internal/config"
)

func TestServerStartAndShutdown(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.HTTP.Port = 0
	cfg.TCP.Port = 0
	cfg.DeviceDirectory = filepath.Join(dir, "devices")
	cfg.DeviceClaimsDirectory = filepath.Join(dir, "deviceClaims")
	cfg.EventsDirectory = filepath.Join(dir, "events")
	cfg.FirmwareDirectory = filepath.Join(dir, "knownApps")
	cfg.UsersDirectory = filepath.Join(dir, "users")
	cfg.TokensDirectory = filepath.Join(dir, "accessTokens")
	cfg.WebhooksDirectory = filepath.Join(dir, "webhooks")
	cfg.ServerKeysDirectory = filepath.Join(dir, "data")

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	server := app.New(cfg, logger)

	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start server: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown server: %v", err)
	}

	output := logs.String()
	for _, message := range []string{
		"msg=\"http listener started\" server=http address=",
		"msg=\"tcp listener started\" server=tcp address=",
		"msg=\"spark server started\" http=",
	} {
		if !strings.Contains(output, message) {
			t.Fatalf("logs do not contain %q:\n%s", message, output)
		}
	}
	if strings.Contains(output, "0.0.0.0:") || strings.Contains(output, "[::]:") {
		t.Fatalf("logs contain a wildcard listener address:\n%s", output)
	}

	for _, path := range []string{
		filepath.Join(cfg.ServerKeysDirectory, "default_key.pem"),
		filepath.Join(cfg.ServerKeysDirectory, "default_key.pub.pem"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing generated key %s: %v", path, err)
		}
	}
}
