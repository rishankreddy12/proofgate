// Package auth provides enterprise-grade capabilities, configuration, and structural components for the auth subsystem.
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

// KeyLookup defines the dependency interface for querying a backing data store (e.g., PostgreSQL/Redis)
// to fetch an API key configuration based strictly on its SHA-256 digest hash.
type KeyLookup interface {
	KeyByHash(ctx context.Context, hash []byte) (store.KeyRecord, error)
}

// Middleware orchestrates API Key validation, authorization, and caching.
// It incorporates defense-in-depth mechanisms:
// 1. Positive/Negative Caching: Uses LRU caches to dramatically reduce DB load for hot keys.
// 2. Cache Stampede Protection: Uses `singleflight` to collapse concurrent identical DB lookups.
// 3. Brute Force Protection: Employs a Token Bucket rate limiter to throttle rapidly failing IPs.
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

// NewMiddleware constructs an Auth Middleware with default cache sizing.
func NewMiddleware(l KeyLookup, ttl, negativeTTL time.Duration) *Middleware {
	return NewMiddlewareWithConfig(l, ttl, negativeTTL, 10_000)
}

// NewMiddlewareWithConfig constructs an Auth Middleware with explicitly tuned LRU cache capacities.
// The negative cache (tracking failed login hashes) is deliberately bounded to prevent malicious
// actors from intentionally thrashing the application's memory by sending millions of unique garbage hashes.
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
		failedAuthRate:  rate.Every(time.Minute / 20), // Hardcoded default: 20 failures per minute per IP
		failedAuthBurst: 20,
	}
}

// SetFailedAuthLimiter dynamically updates the token-bucket parameters used to punish malicious IPs.
// Altering this purges the current history of rate limiters to immediately apply the new policy rules.
func (m *Middleware) SetFailedAuthLimiter(r rate.Limit, burst int) {
	m.limiterMu.Lock()
	defer m.limiterMu.Unlock()
	m.failedAuthRate = r
	m.failedAuthBurst = burst
	m.failedLimiters.Purge()
}

// getLimiter lazily provisions and retrieves a token bucket rate limiter tied to a specific client IP.
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

// clientIP securely resolves the IP address of the requesting client.
// It defers to a configurable `ClientIPFunc` (e.g. for reading X-Forwarded-For securely behind known ELBs).
// If none is provided, it falls back to the native TCP remote address.
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

// bearer extracts the API key from standard OAuth2 Bearer Authorization headers,
// or falls back to the `api-key` header pattern commonly used by Azure/Semantic Kernel SDKs.
func bearer(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) >= 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(r.Header.Get("api-key")) // Azure-style clients
}

// resolve executes the full authentication resolution chain:
// 1. In-memory fast path (LRU hit for Positive or Negative cache).
// 2. Singleflight synchronization to collapse concurrent lookups for the same exact key.
// 3. Heavy database retrieval.
// 4. State hydration back into the appropriate caching tiers.
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
				m.negCache.Set(h, struct{}{}) // Populate Negative Cache
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
		m.posCache.Set(h, p) // Populate Positive Cache
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

// Handler returns the fully-formed HTTP middleware function that enforces Gateway Authentication.
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

		// Fast Path: Immediate Cache Resolution
		h := string(HashKey(key))
		if p, hit := m.posCache.Get(h); hit {
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
			return
		}

		// Anti-Bruteforce Lock Check
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

		// Slow Path: Database Resolution
		p, ok, err := m.resolve(r.Context(), key)
		if err != nil {
			slog.Error("key lookup failed", "err", err) // Security Note: the raw API key is explicitly NEVER logged here
			api.WriteError(w, &api.Error{Status: 503, Message: "auth backend unavailable", Type: "api_error", Code: "auth_unavailable"})
			return
		}
		if !ok {
			// Failed authentication, deduct a token from the IP's limit
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

		// Injection into context to pass to application routers
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// EvictKey forcibly removes a specific API key (by its digest hash) from both positive and negative memory caches.
// Used via webhook when a key is deleted/revoked in the Admin Control Plane to prevent stale access.
func (m *Middleware) EvictKey(keyHash string) {
	m.posCache.Remove(keyHash)
	m.negCache.Remove(keyHash)
}

// Purge triggers an aggressive eviction of all entries from both positive and negative auth caches.
func (m *Middleware) Purge() {
	m.posCache.Purge()
	m.negCache.Purge()
}
