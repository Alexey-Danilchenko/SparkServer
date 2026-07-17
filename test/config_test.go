// Package test verifies legacy-compatible configuration loading.
package test

import (
	"os"
	"path/filepath"
	"testing"

	"sparkserver/internal/config"
)

func TestLoadSettingsJSONWithDefaults(t *testing.T) {
	t.Setenv("TZ", "UTC")

	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	settings := []byte(`{
		"DEFAULT_ADMIN_USERNAME": "admin",
		"EXPRESS_SERVER_CONFIG": {
			"HOST": "127.0.0.1",
			"PORT": 18080
		}
	}`)

	if err := os.WriteFile(settingsPath, settings, 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	cfg, err := config.Load(settingsPath)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}

	if cfg.DefaultAdminUsername != "admin" {
		t.Fatalf("DefaultAdminUsername = %q", cfg.DefaultAdminUsername)
	}
	if cfg.HTTP.Address() != "127.0.0.1:18080" {
		t.Fatalf("HTTP address = %q", cfg.HTTP.Address())
	}
	if cfg.TCP.Address() != "0.0.0.0:5683" {
		t.Fatalf("TCP address = %q", cfg.TCP.Address())
	}
	if cfg.DB.Type != "file" {
		t.Fatalf("DB type = %q", cfg.DB.Type)
	}
	if cfg.APITimeout != 30000 {
		t.Fatalf("API timeout = %d", cfg.APITimeout)
	}
}

func TestLoadSettingsJSONWithAPITimeout(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	settings := []byte(`{"API_TIMEOUT": 1500}`)

	if err := os.WriteFile(settingsPath, settings, 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	cfg, err := config.Load(settingsPath)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if cfg.APITimeout != 1500 {
		t.Fatalf("API timeout = %d", cfg.APITimeout)
	}
}

func TestEnsureDirectories(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DeviceDirectory = filepath.Join(dir, "devices")
	cfg.DeviceClaimsDirectory = filepath.Join(dir, "deviceClaims")
	cfg.EventsDirectory = filepath.Join(dir, "events")
	cfg.FirmwareDirectory = filepath.Join(dir, "knownApps")
	cfg.UsersDirectory = filepath.Join(dir, "users")
	cfg.TokensDirectory = filepath.Join(dir, "accessTokens")
	cfg.WebhooksDirectory = filepath.Join(dir, "webhooks")
	cfg.ServerKeysDirectory = filepath.Join(dir, "data")

	if err := cfg.EnsureDirectories(); err != nil {
		t.Fatalf("ensure directories: %v", err)
	}

	for _, directory := range []string{
		cfg.DeviceDirectory,
		cfg.DeviceClaimsDirectory,
		cfg.EventsDirectory,
		cfg.FirmwareDirectory,
		filepath.Join(cfg.FirmwareDirectory, "metadata"),
		filepath.Join(cfg.FirmwareDirectory, "binaries"),
		filepath.Join(cfg.FirmwareDirectory, "flashJobs"),
		cfg.UsersDirectory,
		cfg.TokensDirectory,
		cfg.WebhooksDirectory,
		cfg.ServerKeysDirectory,
		filepath.Join(cfg.ServerKeysDirectory, "deviceKeys"),
	} {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatalf("stat %s: %v", directory, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", directory)
		}
	}
}
