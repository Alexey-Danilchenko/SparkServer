// Package test verifies application wiring and end-to-end server lifecycle behavior.
package test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sparkserver/internal/app"
	"sparkserver/internal/config"
)

func TestServerStartAndShutdown(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.HTTP.Host = "127.0.0.1"
	cfg.HTTP.Port = 0
	cfg.TCP.Host = "127.0.0.1"
	cfg.TCP.Port = 0
	cfg.DeviceDirectory = filepath.Join(dir, "devices")
	cfg.DeviceClaimsDirectory = filepath.Join(dir, "deviceClaims")
	cfg.EventsDirectory = filepath.Join(dir, "events")
	cfg.FirmwareDirectory = filepath.Join(dir, "knownApps")
	cfg.UsersDirectory = filepath.Join(dir, "users")
	cfg.TokensDirectory = filepath.Join(dir, "accessTokens")
	cfg.WebhooksDirectory = filepath.Join(dir, "webhooks")
	cfg.ServerKeysDirectory = filepath.Join(dir, "data")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := app.New(cfg, logger)

	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start server: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown server: %v", err)
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
