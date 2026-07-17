// Package session stores device session state and encrypts/decrypts session frames.
package session

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
)

const (
	AES128KeySize = 16
	HMACSHA1Size  = 20
)

var (
	ErrInvalidKey     = errors.New("invalid session key")
	ErrInvalidMessage = errors.New("invalid encrypted message")
	ErrInvalidMAC     = errors.New("invalid message authentication code")
)

// Codec applies AES-128-CBC encryption plus HMAC-SHA1 authentication.
type Codec struct {
	encryptionKey []byte
	macKey        []byte
}

// NewCodecFromSession derives codec keys from a negotiated session.
func NewCodecFromSession(session *Session) (*Codec, error) {
	if session == nil {
		return nil, fmt.Errorf("%w: session is nil", ErrInvalidKey)
	}

	return NewCodec(session.SessionKey, session.SessionKey)
}

// NewCodec validates and copies encryption/MAC keys.
func NewCodec(encryptionKey []byte, macKey []byte) (*Codec, error) {
	if len(encryptionKey) != AES128KeySize {
		return nil, fmt.Errorf("%w: AES-128 key must be %d bytes", ErrInvalidKey, AES128KeySize)
	}
	if len(macKey) == 0 {
		return nil, fmt.Errorf("%w: HMAC key is empty", ErrInvalidKey)
	}

	return &Codec{
		encryptionKey: append([]byte(nil), encryptionKey...),
		macKey:        append([]byte(nil), macKey...),
	}, nil
}

// Encrypt pads, encrypts, and authenticates a plaintext CoAP packet.
func (codec *Codec) Encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(codec.encryptionKey)
	if err != nil {
		return nil, err
	}

	iv := make([]byte, block.BlockSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}

	padded := pkcs7Pad(plaintext, block.BlockSize())
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	message := make([]byte, 0, len(iv)+len(ciphertext)+HMACSHA1Size)
	message = append(message, iv...)
	message = append(message, ciphertext...)
	message = append(message, codec.mac(message)...)
	return message, nil
}

// Decrypt verifies the MAC before decrypting and unpadding plaintext.
func (codec *Codec) Decrypt(message []byte) ([]byte, error) {
	block, err := aes.NewCipher(codec.encryptionKey)
	if err != nil {
		return nil, err
	}

	blockSize := block.BlockSize()
	if len(message) < blockSize+HMACSHA1Size+blockSize {
		return nil, fmt.Errorf("%w: too short", ErrInvalidMessage)
	}

	authenticatedLength := len(message) - HMACSHA1Size
	authenticated := message[:authenticatedLength]
	messageMAC := message[authenticatedLength:]
	expectedMAC := codec.mac(authenticated)
	if !hmac.Equal(messageMAC, expectedMAC) {
		return nil, ErrInvalidMAC
	}

	iv := authenticated[:blockSize]
	ciphertext := authenticated[blockSize:]
	if len(ciphertext)%blockSize != 0 {
		return nil, fmt.Errorf("%w: ciphertext is not block aligned", ErrInvalidMessage)
	}

	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
	return pkcs7Unpad(plaintext, blockSize)
}

func (codec *Codec) mac(data []byte) []byte {
	hash := hmac.New(sha1.New, codec.macKey)
	hash.Write(data)
	return hash.Sum(nil)
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padded := make([]byte, 0, len(data)+padding)
	padded = append(padded, data...)
	padded = append(padded, bytes.Repeat([]byte{byte(padding)}, padding)...)
	return padded
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("%w: invalid padding length", ErrInvalidMessage)
	}

	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, fmt.Errorf("%w: invalid padding", ErrInvalidMessage)
	}

	for _, value := range data[len(data)-padding:] {
		if int(value) != padding {
			return nil, fmt.Errorf("%w: invalid padding bytes", ErrInvalidMessage)
		}
	}

	return data[:len(data)-padding], nil
}
