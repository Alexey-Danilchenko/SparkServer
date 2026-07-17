// Package auth contains password hashing and token-backed user authentication.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

const passwordHashPrefix = "sha256"

// HashPassword stores local-cloud passwords as salted SHA-256 hashes.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	sum := passwordSum(salt, password)
	return fmt.Sprintf("%s:%s:%s", passwordHashPrefix, hex.EncodeToString(salt), hex.EncodeToString(sum[:])), nil
}

// VerifyPassword compares a stored password hash without leaking timing information.
func VerifyPassword(hash string, password string) bool {
	parts := strings.Split(hash, ":")
	if len(parts) != 3 || parts[0] != passwordHashPrefix {
		return false
	}

	salt, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	expected, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}

	actual := passwordSum(salt, password)
	return subtle.ConstantTimeCompare(actual[:], expected) == 1
}

func passwordSum(salt []byte, password string) [32]byte {
	data := make([]byte, 0, len(salt)+len(password))
	data = append(data, salt...)
	data = append(data, password...)
	return sha256.Sum256(data)
}
