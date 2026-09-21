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

type KEK interface {
	ID() string
	Wrap(ctx context.Context, dek []byte) ([]byte, error)
	Unwrap(ctx context.Context, wrapped []byte) ([]byte, error)
}

type Sealed struct {
	KEKID      string
	WrappedDEK []byte
	Nonce      []byte
	Ciphertext []byte
}

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

func Open(ctx context.Context, kek KEK, s Sealed, aad []byte) ([]byte, error) {
	if s.KEKID != kek.ID() {
		return nil, fmt.Errorf("credential was sealed with a different KEK (%s, current %s): rewrap it first", s.KEKID, kek.ID())
	}
	dek, err := kek.Unwrap(ctx, s.WrappedDEK)
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

func Rewrap(ctx context.Context, from, to KEK, s Sealed) (Sealed, error) {
	if s.KEKID != from.ID() {
		return Sealed{}, errors.New("record is not sealed with the source KEK")
	}
	dek, err := from.Unwrap(ctx, s.WrappedDEK)
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
