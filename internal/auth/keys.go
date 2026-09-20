// Package auth issues, hashes and verifies ProofGate API keys.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"slices"

	"github.com/proofgate/proofgate/internal/store"
)

// GenerateKey returns a new key "pg_<env>_<secret>", its display prefix and its SHA-256 hash.
// Only the hash is stored; the plaintext is shown once.
func GenerateKey(env string) (plaintext, prefix string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", nil, err
	}
	plaintext = "pg_" + env + "_" + base64.RawURLEncoding.EncodeToString(b)
	return plaintext, plaintext[:12], HashKey(plaintext), nil
}

func HashKey(plaintext string) []byte {
	h := sha256.Sum256([]byte(plaintext))
	return h[:]
}

type Principal struct {
	TenantID      string
	TenantName    string
	KeyID         string
	KeyName       string
	AllowedRoutes []string
	AllowDirect   bool
	Tenant        store.TenantPolicy
	Key           store.KeyPolicy
}

func (p Principal) CanUseRoute(name string) bool {
	return len(p.AllowedRoutes) == 0 || slices.Contains(p.AllowedRoutes, name)
}

type ctxKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}
