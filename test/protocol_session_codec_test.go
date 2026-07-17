// Package test verifies session encryption, MAC, and padding checks.
package test

import (
	"bytes"
	"errors"
	"testing"

	"sparkserver/internal/protocol/session"
)

func TestSessionCodecEncryptDecrypt(t *testing.T) {
	codec, err := session.NewCodec([]byte("0123456789abcdef"), []byte("mac-secret"))
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}

	plaintext := []byte("hello particle")
	message, err := codec.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(message, plaintext) {
		t.Fatal("ciphertext contains plaintext")
	}

	decrypted, err := codec.Decrypt(message)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted = %q", decrypted)
	}
}

func TestSessionCodecRejectsTamperedMessage(t *testing.T) {
	codec, err := session.NewCodec([]byte("0123456789abcdef"), []byte("mac-secret"))
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}

	message, err := codec.Encrypt([]byte("hello particle"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	message[len(message)-1] ^= 0xff
	if _, err := codec.Decrypt(message); !errors.Is(err, session.ErrInvalidMAC) {
		t.Fatalf("decrypt error = %v", err)
	}
}

func TestSessionCodecRejectsBadKey(t *testing.T) {
	if _, err := session.NewCodec([]byte("short"), []byte("mac-secret")); !errors.Is(err, session.ErrInvalidKey) {
		t.Fatalf("new codec error = %v", err)
	}
}
