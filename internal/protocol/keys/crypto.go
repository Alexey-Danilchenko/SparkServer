// Package keys wraps RSA helpers used by device provisioning and handshakes.
package keys

import (
	"crypto/rand"
	"crypto/rsa"
)

// DecryptPKCS1v15 decrypts device-provided session material.
func DecryptPKCS1v15(privateKey *rsa.PrivateKey, ciphertext []byte) ([]byte, error) {
	return rsa.DecryptPKCS1v15(rand.Reader, privateKey, ciphertext)
}

// EncryptPKCS1v15 encrypts test/client session material for a server public key.
func EncryptPKCS1v15(publicKey *rsa.PublicKey, plaintext []byte) ([]byte, error) {
	return rsa.EncryptPKCS1v15(rand.Reader, publicKey, plaintext)
}
