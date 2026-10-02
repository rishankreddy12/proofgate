package auth

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/store"
	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
)

type KeyLookup interface {
	KeyByHash(ctx context.Context, hash []byte) (store.KeyRecord, error)
}

type Middleware struct {
	lookup          KeyLookup
	ttl, negTTL     time.Duration
	posCache        *LRUCache[Principal]
	negCache        *LRUCache[struct{}]
	sfg             singleflight.Group
	failedLimiters  *LRUCache[*rate.Limiter]
	failedAuthRate  rate.Limit
	failedAuthBurst int
	limiterMu       sync.Mutex
	ClientIPFunc    func(r *http.Request) string
}

func NewMiddleware(l KeyLookup, ttl, negativeTTL time.Duration) *Middleware {
	return NewMiddlewareWithConfig(l, ttl, negativeTTL, 10_000)
}

func NewMiddlewareWithConfig(l KeyLookup, ttl, negativeTTL time.Duration, maxEntries int) *Middleware {
	if maxEntries <= 0 {
		maxEntries = 10_000
	}
	negCapacity := 2_000
	if maxEntries < 2_000 {
		negCapacity = maxEntries
	}
	return &Middleware{
		lookup:          l,
		ttl:             ttl,
		negTTL:          negativeTTL,
		posCache:        NewLRU[Principal](maxEntries, ttl),
		negCache:        NewLRU[struct{}](negCapacity, negativeTTL),
		failedLimiters:  NewLRU[*rate.Limiter](10_000, 10*time.Minute),
		failedAuthRate:  rate.Every(time.Minute / 20), // 20 failures per minute
		failedAuthBurst: 20,
	}
}

// SetFailedAuthLimiter updates the rate and burst for the per-IP failed authentication limiter.
func (m *Middleware) SetFailedAuthLimiter(r rate.Limit, burst int) {
	m.limiterMu.Lock()
	defer m.limiterMu.Unlock()
	m.failedAuthRate = r
	m.failedAuthBurst = burst
	m.failedLimiters.Purge()
}

func (m *Middleware) getLimiter(ip string) *rate.Limiter {
	m.limiterMu.Lock()
	defer m.limiterMu.Unlock()
	if lim, ok := m.failedLimiters.Get(ip); ok {
		return lim
	}
	lim := rate.NewLimiter(m.failedAuthRate, m.failedAuthBurst)
	m.failedLimiters.Set(ip, lim)
	return lim
}

func (m *Middleware) clientIP(r *http.Request) string {
	if m.ClientIPFunc != nil {
		if ip := m.ClientIPFunc(r); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func bearer(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) >= 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(r.Header.Get("api-key")) // Azure-style clients
}

func (m *Middleware) resolve(ctx context.Context, key string) (Principal, bool, error) {
	h := string(HashKey(key))

	if p, hit := m.posCache.Get(h); hit {
		return p, true, nil
	}
	if _, hit := m.negCache.Get(h); hit {
		return Principal{}, false, nil
	}

	res, err, _ := m.sfg.Do(h, func() (any, error) {
		// Double check inside singleflight
		if p, hit := m.posCache.Get(h); hit {
			return p, nil
		}
		if _, hit := m.negCache.Get(h); hit {
			return Principal{}, store.ErrNotFound
		}

		rec, dbErr := m.lookup.KeyByHash(ctx, []byte(h))
		if dbErr != nil {
			if errors.Is(dbErr, store.ErrNotFound) {
				m.negCache.Set(h, struct{}{})
				return Principal{}, store.ErrNotFound
			}
			return Principal{}, dbErr
		}

		p := Principal{
			TenantID:      rec.Tenant.ID,
			TenantName:    rec.Tenant.Name,
			KeyID:         rec.Key.ID,
			KeyName:       rec.Key.Name,
			AllowedRoutes: rec.Key.AllowedRoutes,
			AllowDirect:   rec.Key.Policy.AllowDirect,
			Tenant:        rec.Tenant.Policy,
			Key:           rec.Key.Policy,
		}
		m.posCache.Set(h, p)
		return p, nil
	})

	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Principal{}, false, nil
		}
		return Principal{}, false, err
	}
	return res.(Principal), true, nil
}

func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := m.clientIP(r)
		key := bearer(r)

		if key == "" || !strings.HasPrefix(key, "pg_") {
			lim := m.getLimiter(ip)
			if !lim.Allow() {
				api.WriteError(w, &api.Error{
					Status:  http.StatusTooManyRequests,
					Message: "too many failed authentication attempts",
					Type:    "rate_limit_error",
					Code:    "failed_auth_limit_exceeded",
				})
				return
			}
			w.Header().Set("WWW-Authenticate", "Bearer")
			api.WriteError(w, api.Unauthorized())
			return
		}

		h := string(HashKey(key))
		if p, hit := m.posCache.Get(h); hit {
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
			return
		}

		lim := m.getLimiter(ip)
		if lim.Tokens() < 1.0 {
			api.WriteError(w, &api.Error{
				Status:  http.StatusTooManyRequests,
				Message: "too many failed authentication attempts",
				Type:    "rate_limit_error",
				Code:    "failed_auth_limit_exceeded",
			})
			return
		}

		p, ok, err := m.resolve(r.Context(), key)
		if err != nil {
			slog.Error("key lookup failed", "err", err) // never log the key
			api.WriteError(w, &api.Error{Status: 503, Message: "auth backend unavailable", Type: "api_error", Code: "auth_unavailable"})
			return
		}
		if !ok {
			if !lim.Allow() {
				api.WriteError(w, &api.Error{
					Status:  http.StatusTooManyRequests,
					Message: "too many failed authentication attempts",
					Type:    "rate_limit_error",
					Code:    "failed_auth_limit_exceeded",
				})
				return
			}
			w.Header().Set("WWW-Authenticate", "Bearer")
			api.WriteError(w, api.Unauthorized())
			return
		}

		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// EvictKey removes the given key hash from both positive and negative auth caches.
func (m *Middleware) EvictKey(keyHash string) {
	m.posCache.Remove(keyHash)
	m.negCache.Remove(keyHash)
}

// Purge evicts all entries from both positive and negative auth caches.
func (m *Middleware) Purge() {
	m.posCache.Purge()
	m.negCache.Purge()
}
