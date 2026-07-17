// Package handshake establishes the encrypted device session from the first TCP frame.
package handshake

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	"sparkserver/internal/protocol/framing"
	protocolkeys "sparkserver/internal/protocol/keys"
	"sparkserver/internal/protocol/session"
)

// CurrentVersion is the local handshake frame version.
const CurrentVersion byte = 1

var (
	// ErrInvalidFrame reports malformed handshake data.
	ErrInvalidFrame = errors.New("invalid handshake frame")
	// ErrUnknownVersion reports a handshake version this server does not support.
	ErrUnknownVersion = errors.New("unknown handshake version")
)

// KeyManager supplies the server RSA private key used to decrypt session material.
type KeyManager interface {
	LoadServerPrivateKey() (*rsa.PrivateKey, error)
}

// Request is the parsed device hello before session decryption.
type Request struct {
	Version             byte
	DeviceID            string
	EncryptedSessionKey []byte
}

// Handshaker turns the framed device hello into a protocol session.
type Handshaker struct {
	keys KeyManager
}

// NewHandshaker binds handshake processing to a key manager.
func NewHandshaker(keys KeyManager) *Handshaker {
	return &Handshaker{keys: keys}
}

// Handshake reads one frame, decrypts the session key, and returns a session.
func (handshaker *Handshaker) Handshake(
	ctx    context.Context,
	reader io.Reader,
) (*session.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	frame, err := framing.NewReader(reader, framing.DefaultMaxFrameSize).ReadFrame()
	if err != nil {
		return nil, err
	}

	request, err := ParseRequest(frame)
	if err != nil {
		return nil, err
	}

	privateKey, err := handshaker.keys.LoadServerPrivateKey()
	if err != nil {
		return nil, err
	}

	sessionKey, err := protocolkeys.DecryptPKCS1v15(privateKey, request.EncryptedSessionKey)
	if err != nil {
		return nil, err
	}

	return &session.Session{
		DeviceID:   request.DeviceID,
		SessionKey: sessionKey,
	}, nil
}

// MarshalRequest builds a test/client handshake frame.
func MarshalRequest(request Request) ([]byte, error) {
	if request.Version == 0 {
		request.Version = CurrentVersion
	}
	if request.Version != CurrentVersion {
		return nil, fmt.Errorf("%w: %d", ErrUnknownVersion, request.Version)
	}
	if err := validateDeviceID(request.DeviceID); err != nil {
		return nil, err
	}
	if len(request.EncryptedSessionKey) == 0 || len(request.EncryptedSessionKey) > 0xffff {
		return nil, fmt.Errorf("%w: invalid encrypted session key length", ErrInvalidFrame)
	}
	if len(request.DeviceID) > 0xff {
		return nil, fmt.Errorf("%w: device id too long", ErrInvalidFrame)
	}

	var buffer bytes.Buffer
	buffer.WriteByte(request.Version)
	buffer.WriteByte(byte(len(request.DeviceID)))
	buffer.WriteString(request.DeviceID)
	if err := binary.Write(&buffer, binary.BigEndian, uint16(len(request.EncryptedSessionKey))); err != nil {
		return nil, err
	}
	buffer.Write(request.EncryptedSessionKey)
	return buffer.Bytes(), nil
}

// ParseRequest validates and decodes a device handshake frame.
func ParseRequest(frame []byte) (*Request, error) {
	if len(frame) < 4 {
		return nil, fmt.Errorf("%w: too short", ErrInvalidFrame)
	}

	version := frame[0]
	if version != CurrentVersion {
		return nil, fmt.Errorf("%w: %d", ErrUnknownVersion, version)
	}

	deviceIDLength := int(frame[1])
	if deviceIDLength == 0 {
		return nil, fmt.Errorf("%w: missing device id", ErrInvalidFrame)
	}

	offset := 2
	if len(frame) < offset+deviceIDLength+2 {
		return nil, fmt.Errorf("%w: truncated device id", ErrInvalidFrame)
	}

	deviceID := string(frame[offset : offset+deviceIDLength])
	if err := validateDeviceID(deviceID); err != nil {
		return nil, err
	}
	offset += deviceIDLength

	sessionKeyLength := int(binary.BigEndian.Uint16(frame[offset : offset+2]))
	offset += 2
	if sessionKeyLength == 0 {
		return nil, fmt.Errorf("%w: missing encrypted session key", ErrInvalidFrame)
	}
	if len(frame) != offset+sessionKeyLength {
		return nil, fmt.Errorf("%w: encrypted session key length mismatch", ErrInvalidFrame)
	}

	return &Request{
		Version:             version,
		DeviceID:            deviceID,
		EncryptedSessionKey: append([]byte(nil), frame[offset:]...),
	}, nil
}

func validateDeviceID(deviceID string) error {
	if deviceID == "" || deviceID == "." || deviceID == ".." {
		return fmt.Errorf("%w: invalid device id %q", ErrInvalidFrame, deviceID)
	}
	if strings.ContainsAny(deviceID, `/\`) {
		return fmt.Errorf("%w: invalid device id %q", ErrInvalidFrame, deviceID)
	}
	return nil
}
