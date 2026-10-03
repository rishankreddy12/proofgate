// Package auth implements the core authentication mechanisms for the API Gateway.
// It is responsible for generating, hashing, and validating ProofGate API keys,
// as well as enforcing Route-Based Access Control (RBAC) semantics for incoming requests.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"slices"

	"github.com/proofgate/proofgate/internal/store"
)

// GenerateKey securely generates a new API key string using a cryptographically secure
// random number generator (CSPRNG).
//
// Returns:
// - plaintext: The full API key intended to be shown to the user exactly once (e.g., "pg_live_ABC123").
// - prefix: A stable truncated identifier (first 12 characters) for UI display and audit logging.
// - hash: The SHA-256 digest of the plaintext key, strictly used for database persistence.
//
// Security Note: The plaintext must NEVER be logged, serialized, or persisted to disk.
func GenerateKey(env string) (plaintext, prefix string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", nil, err
	}
	plaintext = "pg_" + env + "_" + base64.RawURLEncoding.EncodeToString(b)
	return plaintext, plaintext[:12], HashKey(plaintext), nil
}

// HashKey produces a deterministic, one-way SHA-256 digest of the plaintext API key.
// ProofGate intentionally does not use slow hashing algorithms (like bcrypt or Argon2) here
// because API keys have extremely high entropy (256-bit CSPRNG) making offline dictionary
// attacks computationally infeasible, and slow hashes would induce severe latency on every API request.
func HashKey(plaintext string) []byte {
	h := sha256.Sum256([]byte(plaintext))
	return h[:]
}

// Principal represents the resolved identity and capabilities of an authenticated caller.
// Once an API key is validated, this struct is injected into the HTTP Request Context,
// providing downstream handlers with O(1) access to the caller's identity and RBAC policies.
type Principal struct {
	TenantID      string             // The globally unique identifier for the owning tenant
	TenantName    string             // The human-readable name of the tenant
	KeyID         string             // The globally unique identifier of the specific API key used
	KeyName       string             // The human-readable name assigned to the API key
	AllowedRoutes []string           // The explicit list of route names this key is authorized to invoke
	AllowDirect   bool               // True if the key bypasses the router and can specify raw provider target IDs
	Tenant        store.TenantPolicy // Hierarchical rate limits and budgets bound to the tenant
	Key           store.KeyPolicy    // Leaf rate limits bound specifically to this API key
}

// CanUseRoute evaluates whether the authenticated Principal is authorized to invoke a specific route.
// An empty AllowedRoutes list implies unrestricted global access.
func (p Principal) CanUseRoute(name string) bool {
	return len(p.AllowedRoutes) == 0 || slices.Contains(p.AllowedRoutes, name)
}

// ctxKey is a strongly-typed, unexported context key to prevent collisions.
type ctxKey struct{}

// WithPrincipal injects a validated Principal into a context.Context object.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext extracts the Principal from a context.Context object.
// Downstream handlers must check the boolean return value to ensure authentication succeeded.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}
