// Package test verifies TCP registry and live command routing.
package test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"

	"sparkserver/internal/devices"
	"sparkserver/internal/protocol/tcp"
	filerepo "sparkserver/internal/repository/file"
)

func TestRegistryRegisterTouchUnregister(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	registry := tcp.NewRegistry()
	connection := registry.Register("device-1", client.RemoteAddr())

	if connection.DeviceID != "device-1" {
		t.Fatalf("device id = %q", connection.DeviceID)
	}
	if registry.Count() != 1 {
		t.Fatalf("count = %d", registry.Count())
	}

	if !registry.Touch("device-1") {
		t.Fatal("touch returned false")
	}

	if _, ok := registry.Get("device-1"); !ok {
		t.Fatal("missing connection")
	}

	registry.Unregister("device-1")
	if registry.Count() != 0 {
		t.Fatalf("count after unregister = %d", registry.Count())
	}
}

func TestTCPRegisterDeviceUpdatesDeviceState(t *testing.T) {
	dir := t.TempDir()
	deviceService := devices.NewService(
		filerepo.NewDeviceRepository(filepath.Join(dir, "devices")),
		filerepo.NewDeviceClaimRepository(filepath.Join(dir, "deviceClaims")),
	)

	client, serverConn := net.Pipe()
	defer client.Close()
	defer serverConn.Close()

	tcpServer := tcp.New("127.0.0.1:0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	tcpServer.SetDeviceStatusUpdater(deviceService)

	cleanup := tcpServer.RegisterDevice(context.Background(), "device-1", serverConn)

	if tcpServer.Registry().Count() != 1 {
		t.Fatalf("registry count = %d", tcpServer.Registry().Count())
	}

	device, err := deviceService.Get(context.Background(), "", "device-1")
	if err != nil {
		t.Fatalf("get connected device: %v", err)
	}
	if !device.Connected {
		t.Fatal("device was not marked connected")
	}
	if device.LastHeardAt == nil {
		t.Fatal("missing last heard timestamp")
	}

	cleanup()

	if tcpServer.Registry().Count() != 0 {
		t.Fatalf("registry count after cleanup = %d", tcpServer.Registry().Count())
	}

	device, err = deviceService.Get(context.Background(), "", "device-1")
	if err != nil {
		t.Fatalf("get disconnected device: %v", err)
	}
	if device.Connected {
		t.Fatal("device was not marked disconnected")
	}
}
