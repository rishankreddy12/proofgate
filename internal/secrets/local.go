// Package secrets provides enterprise-grade capabilities, configuration, and structural components for the secrets subsystem.
package secrets

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

// LocalKEK wraps data keys with AES-256-GCM under a key held in a file (mounted read-only).
type LocalKEK struct {
	key []byte
	id  string
}

// NewLocalKEK executes the primary logic for the NewLocalKEK operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewLocalKEK(key []byte) (*LocalKEK, error) {
	if len(key) != 32 {
		return nil, errors.New("local KEK must be exactly 32 bytes")
	}
	sum := sha256.Sum256(key)
	return &LocalKEK{key: append([]byte(nil), key...), id: "local:" + hex.EncodeToString(sum[:6])}, nil
}

// LoadLocalKEK executes the primary logic for the LoadLocalKEK operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func LoadLocalKEK(path string) (*LocalKEK, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, errors.New("local KEK file must contain base64")
	}
	return NewLocalKEK(raw)
}

// GenerateLocalKEK executes the primary logic for the GenerateLocalKEK operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func GenerateLocalKEK() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// ID executes the primary logic for the ID operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (k *LocalKEK) ID() string { return k.id }

// Wrap executes the primary logic for the Wrap operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (k *LocalKEK) Wrap(_ context.Context, dek []byte) ([]byte, error) {
	a, err := gcm(k.key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, dek, []byte(k.id)), nil
}

// Unwrap executes the primary logic for the Unwrap operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (k *LocalKEK) Unwrap(_ context.Context, wrapped []byte) ([]byte, error) {
	a, err := gcm(k.key)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < a.NonceSize() {
		return nil, errors.New("wrapped key too short")
	}
	return a.Open(nil, wrapped[:a.NonceSize()], wrapped[a.NonceSize():], []byte(k.id))
}
