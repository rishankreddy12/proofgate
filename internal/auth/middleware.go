package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/store"
)

type KeyLookup interface {
	KeyByHash(ctx context.Context, hash []byte) (store.KeyRecord, error)
}

type cacheEntry struct {
	p       Principal
	ok      bool
	expires time.Time
}

type Middleware struct {
	lookup      KeyLookup
	ttl, negTTL time.Duration
	mu          sync.RWMutex
	cache       map[string]cacheEntry // key: sha256 hash as string
	maxEntries  int
}

func NewMiddleware(l KeyLookup, ttl, negativeTTL time.Duration) *Middleware {
	return &Middleware{lookup: l, ttl: ttl, negTTL: negativeTTL, cache: map[string]cacheEntry{}, maxEntries: 100_000}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if v, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(r.Header.Get("api-key")) // Azure-style clients
}

func (m *Middleware) resolve(ctx context.Context, key string) (Principal, bool, error) {
	h := string(HashKey(key))
	now := time.Now()
	m.mu.RLock()
	e, hit := m.cache[h]
	m.mu.RUnlock()
	if hit && now.Before(e.expires) {
		return e.p, e.ok, nil
	}
	rec, err := m.lookup.KeyByHash(ctx, []byte(h))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return Principal{}, false, err
	}
	entry := cacheEntry{expires: now.Add(m.negTTL)}
	if err == nil {
		entry = cacheEntry{ok: true, expires: now.Add(m.ttl), p: Principal{
			TenantID: rec.Tenant.ID, TenantName: rec.Tenant.Name, KeyID: rec.Key.ID, KeyName: rec.Key.Name,
			AllowedRoutes: rec.Key.AllowedRoutes, AllowDirect: rec.Key.Policy.AllowDirect,
			Tenant: rec.Tenant.Policy, Key: rec.Key.Policy}}
	}
	m.mu.Lock()
	if len(m.cache) >= m.maxEntries {
		m.cache = map[string]cacheEntry{} // simple bound; a flood of bad keys cannot grow memory forever
	}
	m.cache[h] = entry
	m.mu.Unlock()
	return entry.p, entry.ok, nil
}

func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := bearer(r)
		if key == "" || !strings.HasPrefix(key, "pg_") {
			api.WriteError(w, api.Unauthorized())
			return
		}
		p, ok, err := m.resolve(r.Context(), key)
		if err != nil {
			slog.Error("key lookup failed", "err", err) // never log the key
			api.WriteError(w, &api.Error{Status: 503, Message: "auth backend unavailable", Type: "api_error", Code: "auth_unavailable"})
			return
		}
		if !ok {
			api.WriteError(w, api.Unauthorized())
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}
