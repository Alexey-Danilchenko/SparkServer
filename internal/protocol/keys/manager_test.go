// Package test verifies RSA key management and PEM parsing.
package keys_test

import (
	"crypto/rand"
	"crypto/rsa"
	"os"
	"path/filepath"
	"testing"

	protocolkeys "sparkserver/internal/protocol/keys"
)

func TestKeyManagerEnsuresServerKeyPair(t *testing.T) {
	dir := t.TempDir()
	manager := protocolkeys.NewManager(dir)

	if err := manager.EnsureServerKeyPair(); err != nil {
		t.Fatalf("ensure server key pair: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "default_key.pem")); err != nil {
		t.Fatalf("missing private key: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "default_key.pub.pem")); err != nil {
		t.Fatalf("missing public key: %v", err)
	}

	privateKey, err := manager.LoadServerPrivateKey()
	if err != nil {
		t.Fatalf("load private key: %v", err)
	}
	publicKey, err := manager.LoadServerPublicKey()
	if err != nil {
		t.Fatalf("load public key: %v", err)
	}
	if privateKey.PublicKey.N.Cmp(publicKey.N) != 0 {
		t.Fatal("public key does not match private key")
	}
}

func TestDevicePublicKeyRoundTrip(t *testing.T) {
	manager := protocolkeys.NewManager(t.TempDir())
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	if err := manager.SaveDevicePublicKey("device-1", &privateKey.PublicKey); err != nil {
		t.Fatalf("save device key: %v", err)
	}

	publicKey, err := manager.LoadDevicePublicKey("device-1")
	if err != nil {
		t.Fatalf("load device key: %v", err)
	}
	if publicKey.N.Cmp(privateKey.PublicKey.N) != 0 {
		t.Fatal("loaded device key does not match")
	}
}

func TestPKCS1v15RoundTrip(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	plaintext := []byte("session-secret")
	ciphertext, err := protocolkeys.EncryptPKCS1v15(&privateKey.PublicKey, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	decrypted, err := protocolkeys.DecryptPKCS1v15(privateKey, ciphertext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("decrypted = %q", decrypted)
	}
}

func TestDevicePublicKeyRejectsPathTraversal(t *testing.T) {
	manager := protocolkeys.NewManager(t.TempDir())
	if _, err := manager.DevicePublicKeyPath("../bad"); err == nil {
		t.Fatal("expected invalid device id error")
	}
}
