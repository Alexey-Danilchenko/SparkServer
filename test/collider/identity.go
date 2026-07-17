// Package collider generates virtual device identities and handshake credentials.
package collider

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"testing"
)

type Identity struct {
	DeviceID     string
	PrivateKey   *rsa.PrivateKey
	PublicKeyPEM string
}

func NewIdentity(t *testing.T) Identity {
	t.Helper()

	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		t.Fatalf("generate device id: %v", err)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate device key: %v", err)
	}

	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal device public key: %v", err)
	}

	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyDER,
	})
	if publicKeyPEM == nil {
		t.Fatal("encode device public key")
	}

	return Identity{
		DeviceID:     hex.EncodeToString(idBytes),
		PrivateKey:   privateKey,
		PublicKeyPEM: string(publicKeyPEM),
	}
}
