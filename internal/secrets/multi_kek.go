package secrets

import (
	"context"
	"fmt"
	"sync"
)

// MultiKEK manages a default KEK along with per-tenant BYOK (Bring Your Own Key) KEKs.
// It implements KEK, KEKResolver, and provides tenant-scoped encryption operations.
type MultiKEK struct {
	mu         sync.RWMutex
	defaultKEK KEK
	tenants    map[string]KEK // tenantID -> KEK
	byID       map[string]KEK // kekID -> KEK
}

// NewMultiKEK initializes a MultiKEK with a default KEK and optional tenant KEKs.
func NewMultiKEK(defaultKEK KEK, tenants map[string]KEK) *MultiKEK {
	m := &MultiKEK{
		defaultKEK: defaultKEK,
		tenants:    map[string]KEK{},
		byID:       map[string]KEK{},
	}
	if defaultKEK != nil {
		m.byID[defaultKEK.ID()] = defaultKEK
	}
	for tenantID, kek := range tenants {
		if kek != nil {
			m.tenants[tenantID] = kek
			m.byID[kek.ID()] = kek
		}
	}
	return m
}

func (m *MultiKEK) ID() string {
	if m.defaultKEK != nil {
		return m.defaultKEK.ID()
	}
	return "multi-kek"
}

func (m *MultiKEK) Wrap(ctx context.Context, dek []byte) ([]byte, error) {
	if m.defaultKEK == nil {
		return nil, fmt.Errorf("no default KEK configured")
	}
	return m.defaultKEK.Wrap(ctx, dek)
}

func (m *MultiKEK) Unwrap(ctx context.Context, wrapped []byte) ([]byte, error) {
	if m.defaultKEK == nil {
		return nil, fmt.Errorf("no default KEK configured")
	}
	return m.defaultKEK.Unwrap(ctx, wrapped)
}

// ResolveKEK implements KEKResolver by looking up any registered KEK (default or tenant-specific) by ID.
func (m *MultiKEK) ResolveKEK(id string) (KEK, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	k, ok := m.byID[id]
	return k, ok
}

// ForTenant returns the dedicated KEK for a tenant, or the default KEK if none was configured.
func (m *MultiKEK) ForTenant(tenantID string) KEK {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if k, ok := m.tenants[tenantID]; ok {
		return k
	}
	return m.defaultKEK
}

// RegisterTenant registers or updates a tenant-specific BYOK KEK.
func (m *MultiKEK) RegisterTenant(tenantID string, kek KEK) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tenants[tenantID] = kek
	m.byID[kek.ID()] = kek
}

// SealForTenant seals plaintext using the specified tenant's dedicated KEK.
func (m *MultiKEK) SealForTenant(ctx context.Context, tenantID string, plaintext, aad []byte) (Sealed, error) {
	k := m.ForTenant(tenantID)
	if k == nil {
		return Sealed{}, fmt.Errorf("no KEK available for tenant %q", tenantID)
	}
	return Seal(ctx, k, plaintext, aad)
}
