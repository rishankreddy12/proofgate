package secrets

import (
	"context"
	"fmt"
	"sync"
)

// MultiKEK manages the current default KEK and historical KEKs for rotation.
// It implements KEK, KEKResolver, encrypting with the default KEK and decrypting
// with whichever KEK matches the sealed credential's KEK ID.
type MultiKEK struct {
	mu         sync.RWMutex
	defaultKEK KEK
	byID       map[string]KEK // kekID -> KEK
}

// NewMultiKEK initializes a MultiKEK with a current default KEK and optional previous KEKs.
func NewMultiKEK(defaultKEK KEK, previousKEKs ...KEK) *MultiKEK {
	m := &MultiKEK{
		defaultKEK: defaultKEK,
		byID:       make(map[string]KEK),
	}
	if defaultKEK != nil {
		m.byID[defaultKEK.ID()] = defaultKEK
	}
	for _, k := range previousKEKs {
		if k != nil {
			m.byID[k.ID()] = k
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

func (m *MultiKEK) DefaultKEK() KEK {
	return m.defaultKEK
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

// ResolveKEK implements KEKResolver by looking up any registered KEK (default or previous) by ID.
func (m *MultiKEK) ResolveKEK(id string) (KEK, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	k, ok := m.byID[id]
	return k, ok
}

// RegisterKEK adds an older or alternative KEK for resolution during rotation.
func (m *MultiKEK) RegisterKEK(k KEK) {
	if k == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byID[k.ID()] = k
}
