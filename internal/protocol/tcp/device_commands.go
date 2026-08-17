// Package tcp exposes REST-facing live device commands implemented by the TCP server.
package tcp

import (
	"context"

	"sparkserver/internal/firmware"
)

// GetVariable routes a REST variable read to a connected device client.
func (s *Server) GetVariable(
	ctx context.Context,
	deviceID string,
	variableName string,
) (string, error) {
	client, ok := s.registry.GetClient(deviceID)
	if !ok {
		return "", ErrDeviceOffline
	}
	return client.GetVariable(ctx, variableName)
}

func (s *Server) CallFunction(
	ctx context.Context,
	deviceID string,
	functionName string,
	argument string,
) (int, error) {
	client, ok := s.registry.GetClient(deviceID)
	if !ok {
		return 0, ErrDeviceOffline
	}
	return client.CallFunction(ctx, functionName, argument)
}

func (s *Server) Ping(ctx context.Context, deviceID string) error {
	client, ok := s.registry.GetClient(deviceID)
	if !ok {
		return ErrDeviceOffline
	}
	return client.Ping(ctx)
}

// BeginFlash starts OTA negotiation with a connected device.
func (s *Server) BeginFlash(ctx context.Context, deviceID string, job *firmware.FlashJob) error {
	client, ok := s.registry.GetClient(deviceID)
	if !ok {
		return ErrDeviceOffline
	}
	return client.BeginFlash(ctx, job)
}

func (s *Server) SendFlashChunk(
	ctx context.Context,
	deviceID string,
	job *firmware.FlashJob,
	chunk firmware.OTAChunk,
	data []byte,
) error {
	client, ok := s.registry.GetClient(deviceID)
	if !ok {
		return ErrDeviceOffline
	}
	return client.SendFlashChunk(ctx, job, chunk, data)
}

func (s *Server) CompleteFlash(ctx context.Context, deviceID string, job *firmware.FlashJob) error {
	client, ok := s.registry.GetClient(deviceID)
	if !ok {
		return ErrDeviceOffline
	}
	return client.CompleteFlash(ctx, job)
}
