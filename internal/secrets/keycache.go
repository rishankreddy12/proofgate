// Package secrets provides enterprise-grade capabilities, configuration, and structural components for the secrets subsystem.
package secrets

import (
	"context"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/proofgate/proofgate/internal/control"
	"golang.org/x/sync/singleflight"
)

var (
	// KeycacheStaleServedTotal defines a specific variation or structural setting for KeycacheStaleServedTotal.
	KeycacheStaleServedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proofgate_keycache_stale_served_total",
			Help: "Total stale credentials served from KeyCache when upstream store refresh failed.",
		},
		[]string{"provider"},
	)
)

// CredentialSource defines the core enterprise configuration and state for CredentialSource.
// It is responsible for managing the lifecycle, validation, and schema of the CredentialSource entity.
type CredentialSource interface {
	ActiveCredential(ctx context.Context, provider string) (Sealed, int, error)
}

type cached struct {
	value      []byte
	expires    time.Time
	staleUntil time.Time
}

// KeyCache defines the core enterprise configuration and state for KeyCache.
// It is responsible for managing the lifecycle, validation, and schema of the KeyCache entity.
type KeyCache struct {
	src        CredentialSource
	kek        KEK
	ttl        time.Duration
	staleGrace time.Duration
	onSecret   func(string)
	now        func() time.Time
	randFloat  func() float64
	sfg        singleflight.Group
	mu         sync.Mutex
	m          map[string]cached
	epoch      uint64
}

// NewKeyCache executes the primary logic for the NewKeyCache operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewKeyCache(src CredentialSource, kek KEK, ttl time.Duration, onSecret func(string)) *KeyCache {
	return NewKeyCacheWithStaleGrace(src, kek, ttl, 5*time.Minute, onSecret)
}

// NewKeyCacheWithStaleGrace executes the primary logic for the NewKeyCacheWithStaleGrace operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewKeyCacheWithStaleGrace(src CredentialSource, kek KEK, ttl, staleGrace time.Duration, onSecret func(string)) *KeyCache {
	if onSecret == nil {
		onSecret = func(string) {}
	}
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	if staleGrace <= 0 {
		staleGrace = 5 * time.Minute
	}
	return &KeyCache{
		src:        src,
		kek:        kek,
		ttl:        ttl,
		staleGrace: staleGrace,
		onSecret:   onSecret,
		now:        time.Now,
		randFloat:  rand.Float64,
		m:          make(map[string]cached),
	}
}

// Get executes the primary logic for the Get operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (k *KeyCache) Get(ctx context.Context, provider string) (string, error) {
	now := k.now()

	// 1. Fast path: fresh cache hit
	k.mu.Lock()
	if c, ok := k.m[provider]; ok && now.Before(c.expires) {
		val := string(c.value)
		k.mu.Unlock()
		return val, nil
	}
	k.mu.Unlock()

	// 2. Singleflight load + unwrap
	res, err, _ := k.sfg.Do(provider, func() (any, error) {
		// Double check inside singleflight in case a concurrent flight loaded it
		k.mu.Lock()
		if c, ok := k.m[provider]; ok && k.now().Before(c.expires) {
			val := string(c.value)
			k.mu.Unlock()
			return val, nil
		}
		k.mu.Unlock()

		k.mu.Lock()
		loadEpoch := k.epoch
		k.mu.Unlock()
		sfCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		s, _, fetchErr := k.src.ActiveCredential(sfCtx, provider)
		if fetchErr == nil {
			pt, openErr := Open(sfCtx, k.kek, s, AAD(provider))
			if openErr == nil {
				v := string(pt)
				k.onSecret(v)

				// Apply ±10% TTL jitter
				rf := k.randFloat()
				jitterFrac := (rf * 0.20) - 0.10 // in [-0.10, +0.10]
				jitter := time.Duration(float64(k.ttl) * jitterFrac)
				exp := k.now().Add(k.ttl + jitter)
				stale := exp.Add(k.staleGrace)

				k.mu.Lock()
				if k.epoch == loadEpoch {
					k.m[provider] = cached{
						value:      pt,
						expires:    exp,
						staleUntil: stale,
					}
				}
				k.mu.Unlock()
				return v, nil
			}
			fetchErr = openErr
		}

		// Refresh failed: fallback to stale grace if valid
		k.mu.Lock()
		if c, ok := k.m[provider]; ok && k.now().Before(c.staleUntil) {
			val := string(c.value)
			k.mu.Unlock()
			KeycacheStaleServedTotal.WithLabelValues(provider).Inc()
			slog.Warn("serving stale credential from keycache due to refresh error", "provider", provider, "err", fetchErr)
			return val, nil
		}
		k.mu.Unlock()

		return "", fetchErr
	})

	if err != nil {
		return "", err
	}
	return res.(string), nil
}

// Purge executes the primary logic for the Purge operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (k *KeyCache) Purge() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.epoch++
	for _, c := range k.m {
		zero(c.value)
	}
	k.m = make(map[string]cached)
}

// PurgeProvider removes the cached credential for provider and any tenant
// override using the provider suffix (tenant-id/provider). It also invalidates
// a concurrent refresh so it cannot repopulate the old credential.
func (k *KeyCache) PurgeProvider(provider string) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.epoch++
	for name, c := range k.m {
		if name == provider || strings.HasSuffix(name, "/"+provider) {
			zero(c.value)
			delete(k.m, name)
		}
	}
}

// SubscribeControl connects provider credential invalidation to the shared
// control bus. The event carries no credential material.
func (k *KeyCache) SubscribeControl(bus *control.Bus) {
	if k == nil || bus == nil {
		return
	}
	bus.Subscribe(control.OpCredentialInvalidated, func(_ context.Context, msg control.Message) error {
		k.PurgeProvider(msg.Args["provider"])
		return nil
	})
}
