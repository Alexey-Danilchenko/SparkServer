// Package keys manages server and device RSA public/private key files.
package keys

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultPrivateKeyFile = "default_key.pem"
	defaultPublicKeyFile  = "default_key.pub.pem"
	deviceKeysDirectory   = "deviceKeys"
)

// Manager stores server keys and per-device public keys in PEM files.
type Manager struct {
	directory string
}

// NewManager creates a key manager rooted at the configured server key directory.
func NewManager(directory string) *Manager {
	return &Manager{directory: directory}
}

// EnsureServerKeyPair creates the local cloud RSA key pair on first startup.
func (manager *Manager) EnsureServerKeyPair() error {
	privatePath := manager.ServerPrivateKeyPath()
	publicPath := manager.ServerPublicKeyPath()

	if _, err := os.Stat(privatePath); err == nil {
		if _, err := os.Stat(publicPath); err != nil {
			return err
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(manager.DeviceKeysDirectory(), 0o755); err != nil {
		return err
	}

	if err := writePrivateKey(privatePath, privateKey); err != nil {
		return err
	}

	return writePublicKey(publicPath, &privateKey.PublicKey)
}

func (manager *Manager) LoadServerPrivateKey() (*rsa.PrivateKey, error) {
	return readPrivateKey(manager.ServerPrivateKeyPath())
}

func (manager *Manager) LoadServerPublicKey() (*rsa.PublicKey, error) {
	return readPublicKey(manager.ServerPublicKeyPath())
}

func (manager *Manager) SaveDevicePublicKey(deviceID string, publicKey *rsa.PublicKey) error {
	path, err := manager.DevicePublicKeyPath(deviceID)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(manager.DeviceKeysDirectory(), 0o755); err != nil {
		return err
	}

	return writePublicKey(path, publicKey)
}

// SaveDevicePublicKeyPEM validates and stores a key received through provisioning.
func (manager *Manager) SaveDevicePublicKeyPEM(deviceID string, publicKeyPEM string) error {
	publicKey, err := ParsePublicKeyPEM([]byte(publicKeyPEM))
	if err != nil {
		return err
	}
	return manager.SaveDevicePublicKey(deviceID, publicKey)
}

func (manager *Manager) LoadDevicePublicKey(deviceID string) (*rsa.PublicKey, error) {
	path, err := manager.DevicePublicKeyPath(deviceID)
	if err != nil {
		return nil, err
	}

	return readPublicKey(path)
}

func (manager *Manager) ServerPrivateKeyPath() string {
	return filepath.Join(manager.directory, defaultPrivateKeyFile)
}

func (manager *Manager) ServerPublicKeyPath() string {
	return filepath.Join(manager.directory, defaultPublicKeyFile)
}

func (manager *Manager) DeviceKeysDirectory() string {
	return filepath.Join(manager.directory, deviceKeysDirectory)
}

func (manager *Manager) DevicePublicKeyPath(deviceID string) (string, error) {
	if err := validateDeviceID(deviceID); err != nil {
		return "", err
	}
	return filepath.Join(manager.DeviceKeysDirectory(), deviceID+".pub.pem"), nil
}

func writePrivateKey(path string, privateKey *rsa.PrivateKey) error {
	data := x509.MarshalPKCS1PrivateKey(privateKey)
	return writePEM(path, "RSA PRIVATE KEY", data, 0o600)
}

func writePublicKey(path string, publicKey *rsa.PublicKey) error {
	data, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return err
	}
	return writePEM(path, "PUBLIC KEY", data, 0o644)
}

func readPrivateKey(path string) (*rsa.PrivateKey, error) {
	block, err := readPEM(path)
	if err != nil {
		return nil, err
	}
	if block.Type != "RSA PRIVATE KEY" {
		return nil, fmt.Errorf("unexpected private key type %q", block.Type)
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func readPublicKey(path string) (*rsa.PublicKey, error) {
	block, err := readPEM(path)
	if err != nil {
		return nil, err
	}

	return publicKeyFromPEMBlock(block)
}

func ParsePublicKeyPEM(data []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("missing PEM block")
	}
	return publicKeyFromPEMBlock(block)
}

func publicKeyFromPEMBlock(block *pem.Block) (*rsa.PublicKey, error) {
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err == nil {
		publicKey, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("public key is not RSA")
		}
		return publicKey, nil
	}

	publicKey, pkcs1Err := x509.ParsePKCS1PublicKey(block.Bytes)
	if pkcs1Err == nil {
		return publicKey, nil
	}
	return nil, err
}

func writePEM(path string, blockType string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer file.Close()

	return pem.Encode(file, &pem.Block{Type: blockType, Bytes: data})
}

func readPEM(path string) (*pem.Block, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("missing PEM block")
	}

	return block, nil
}

func validateDeviceID(deviceID string) error {
	if deviceID == "" || deviceID == "." || deviceID == ".." {
		return fmt.Errorf("invalid device id %q", deviceID)
	}
	for _, char := range deviceID {
		if char == '/' || char == '\\' {
			return fmt.Errorf("invalid device id %q", deviceID)
		}
	}
	return nil
}
