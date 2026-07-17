// Package test verifies handshake frame parsing and session setup.
package test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"sparkserver/internal/protocol/framing"
	"sparkserver/internal/protocol/handshake"
	protocolkeys "sparkserver/internal/protocol/keys"
)

func TestHandshakeRequestMarshalParse(t *testing.T) {
	request := handshake.Request{
		DeviceID:            "device-1",
		EncryptedSessionKey: []byte{0x01, 0x02, 0x03},
	}

	frame, err := handshake.MarshalRequest(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	parsed, err := handshake.ParseRequest(frame)
	if err != nil {
		t.Fatalf("parse request: %v", err)
	}
	if parsed.Version != handshake.CurrentVersion {
		t.Fatalf("version = %d", parsed.Version)
	}
	if parsed.DeviceID != request.DeviceID {
		t.Fatalf("device id = %q", parsed.DeviceID)
	}
	if !bytes.Equal(parsed.EncryptedSessionKey, request.EncryptedSessionKey) {
		t.Fatalf("encrypted session key = %x", parsed.EncryptedSessionKey)
	}
}

func TestHandshakeRequestRejectsMalformedFrame(t *testing.T) {
	if _, err := handshake.ParseRequest([]byte{0x01}); !errors.Is(err, handshake.ErrInvalidFrame) {
		t.Fatalf("short frame error = %v", err)
	}

	if _, err := handshake.ParseRequest([]byte{0xff, 0x00, 0x00, 0x00}); !errors.Is(err, handshake.ErrUnknownVersion) {
		t.Fatalf("unknown version error = %v", err)
	}

	_, err := handshake.MarshalRequest(handshake.Request{
		DeviceID:            "../bad",
		EncryptedSessionKey: []byte{0x01},
	})
	if !errors.Is(err, handshake.ErrInvalidFrame) {
		t.Fatalf("invalid device id error = %v", err)
	}
}

func TestHandshakerDecryptsSessionKey(t *testing.T) {
	keyManager := protocolkeys.NewManager(t.TempDir())
	if err := keyManager.EnsureServerKeyPair(); err != nil {
		t.Fatalf("ensure key pair: %v", err)
	}

	publicKey, err := keyManager.LoadServerPublicKey()
	if err != nil {
		t.Fatalf("load public key: %v", err)
	}

	sessionKey := []byte("0123456789abcdef")
	encrypted, err := protocolkeys.EncryptPKCS1v15(publicKey, sessionKey)
	if err != nil {
		t.Fatalf("encrypt session key: %v", err)
	}

	payload, err := handshake.MarshalRequest(handshake.Request{
		DeviceID:            "device-1",
		EncryptedSessionKey: encrypted,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var buffer bytes.Buffer
	if err := framing.NewWriter(&buffer).WriteFrame(payload); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	session, err := handshake.NewHandshaker(keyManager).Handshake(context.Background(), &buffer)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}

	if session.DeviceID != "device-1" {
		t.Fatalf("device id = %q", session.DeviceID)
	}
	if !bytes.Equal(session.SessionKey, sessionKey) {
		t.Fatalf("session key = %x", session.SessionKey)
	}
}
