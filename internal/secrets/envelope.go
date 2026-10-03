// Package secrets implements envelope encryption for provider credentials.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KEK defines the core enterprise configuration and state for KEK.
// It is responsible for managing the lifecycle, validation, and schema of the KEK entity.
type KEK interface {
	ID() string
	Wrap(ctx context.Context, dek []byte) ([]byte, error)
	Unwrap(ctx context.Context, wrapped []byte) ([]byte, error)
}

// Sealed defines the core enterprise configuration and state for Sealed.
// It is responsible for managing the lifecycle, validation, and schema of the Sealed entity.
type Sealed struct {
	KEKID      string
	WrappedDEK []byte
	Nonce      []byte
	Ciphertext []byte
}

// AAD executes the primary logic for the AAD operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func AAD(provider string) []byte { return []byte("proofgate:provider:" + provider) }

func gcm(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Seal executes the primary logic for the Seal operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func Seal(ctx context.Context, kek KEK, plaintext, aad []byte) (Sealed, error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return Sealed{}, err
	}
	defer zero(dek)
	a, err := gcm(dek)
	if err != nil {
		return Sealed{}, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Sealed{}, err
	}
	wrapped, err := kek.Wrap(ctx, dek)
	if err != nil {
		return Sealed{}, fmt.Errorf("wrap data key: %w", err)
	}
	return Sealed{KEKID: kek.ID(), WrappedDEK: wrapped, Nonce: nonce, Ciphertext: a.Seal(nil, nonce, plaintext, aad)}, nil
}

// KEKResolver allows a multi-KEK container to resolve the matching KEK by ID.
type KEKResolver interface {
	ResolveKEK(id string) (KEK, bool)
}

// Open executes the primary logic for the Open operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func Open(ctx context.Context, kek KEK, s Sealed, aad []byte) ([]byte, error) {
	targetKEK := kek
	if s.KEKID != kek.ID() {
		if res, ok := kek.(KEKResolver); ok {
			if resolved, found := res.ResolveKEK(s.KEKID); found {
				targetKEK = resolved
			} else {
				return nil, fmt.Errorf("credential was sealed with unknown KEK (%s)", s.KEKID)
			}
		} else {
			return nil, fmt.Errorf("credential was sealed with a different KEK (%s, current %s): rewrap it first", s.KEKID, kek.ID())
		}
	}
	dek, err := targetKEK.Unwrap(ctx, s.WrappedDEK)
	if err != nil {
		return nil, fmt.Errorf("unwrap data key: %w", err)
	}
	defer zero(dek)
	a, err := gcm(dek)
	if err != nil {
		return nil, err
	}
	pt, err := a.Open(nil, s.Nonce, s.Ciphertext, aad)
	if err != nil {
		return nil, errors.New("credential failed authentication (tampered, or wrong provider)")
	}
	return pt, nil
}

// Rewrap executes the primary logic for the Rewrap operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func Rewrap(ctx context.Context, from, to KEK, s Sealed) (Sealed, error) {
	fromKEK := from
	if s.KEKID != from.ID() {
		if res, ok := from.(KEKResolver); ok {
			if resolved, found := res.ResolveKEK(s.KEKID); found {
				fromKEK = resolved
			} else {
				return Sealed{}, errors.New("record is not sealed with the source KEK")
			}
		} else {
			return Sealed{}, errors.New("record is not sealed with the source KEK")
		}
	}
	dek, err := fromKEK.Unwrap(ctx, s.WrappedDEK)
	if err != nil {
		return Sealed{}, err
	}
	defer zero(dek)
	w, err := to.Wrap(ctx, dek)
	if err != nil {
		return Sealed{}, err
	}
	s.KEKID, s.WrappedDEK = to.ID(), w
	return s, nil
}
