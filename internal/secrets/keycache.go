package secrets

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/sync/singleflight"
)

var (
	KeycacheStaleServedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proofgate_keycache_stale_served_total",
			Help: "Total stale credentials served from KeyCache when upstream store refresh failed.",
		},
		[]string{"provider"},
	)
)

type CredentialSource interface {
	ActiveCredential(ctx context.Context, provider string) (Sealed, int, error)
}

type cached struct {
	value      []byte
	expires    time.Time
	staleUntil time.Time
}

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
}

func NewKeyCache(src CredentialSource, kek KEK, ttl time.Duration, onSecret func(string)) *KeyCache {
	return NewKeyCacheWithStaleGrace(src, kek, ttl, 5*time.Minute, onSecret)
}

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

func (k *KeyCache) Get(ctx context.Context, provider string) (string, error) {
	now := k.now()

	// 1. Fast path: fresh cache hit
	k.mu.Lock()
	if c, ok := k.m[provider]; ok && now.Before(c.expires) {
		k.mu.Unlock()
		return string(c.value), nil
	}
	k.mu.Unlock()

	// 2. Singleflight load + unwrap
	res, err, _ := k.sfg.Do(provider, func() (any, error) {
		// Double check inside singleflight in case a concurrent flight loaded it
		k.mu.Lock()
		if c, ok := k.m[provider]; ok && k.now().Before(c.expires) {
			k.mu.Unlock()
			return string(c.value), nil
		}
		k.mu.Unlock()

		s, _, fetchErr := k.src.ActiveCredential(ctx, provider)
		if fetchErr == nil {
			pt, openErr := Open(ctx, k.kek, s, AAD(provider))
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
				k.m[provider] = cached{
					value:      pt,
					expires:    exp,
					staleUntil: stale,
				}
				k.mu.Unlock()
				return v, nil
			}
			fetchErr = openErr
		}

		// Refresh failed: fallback to stale grace if valid
		k.mu.Lock()
		if c, ok := k.m[provider]; ok && k.now().Before(c.staleUntil) {
			k.mu.Unlock()
			KeycacheStaleServedTotal.WithLabelValues(provider).Inc()
			slog.Warn("serving stale credential from keycache due to refresh error", "provider", provider, "err", fetchErr)
			return string(c.value), nil
		}
		k.mu.Unlock()

		return "", fetchErr
	})

	if err != nil {
		return "", err
	}
	return res.(string), nil
}

func (k *KeyCache) Purge() {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, c := range k.m {
		zero(c.value)
	}
	k.m = make(map[string]cached)
}
