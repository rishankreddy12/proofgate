package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

type fakeLookup struct {
	calls atomic.Int32
	keys  map[string]store.KeyRecord
	delay time.Duration
}

func (f *fakeLookup) KeyByHash(_ context.Context, h []byte) (store.KeyRecord, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if r, ok := f.keys[string(h)]; ok {
		return r, nil
	}
	return store.KeyRecord{}, store.ErrNotFound
}

func TestMiddleware(t *testing.T) {
	key, _, hash, _ := GenerateKey("test")
	f := &fakeLookup{keys: map[string]store.KeyRecord{string(hash): {
		Key:    store.APIKey{ID: "k1", Name: "ci", AllowedRoutes: []string{"default"}},
		Tenant: store.Tenant{ID: "t1", Name: "acme", Policy: store.TenantPolicy{RPM: 5}},
	}}}
	mw := NewMiddleware(f, time.Minute, time.Minute)
	var got Principal
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = FromContext(r.Context())
		w.WriteHeader(200)
	}))

	do := func(auth string) (*httptest.ResponseRecorder, int) {
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec, rec.Code
	}

	rec, code := do("")
	require.Equal(t, 401, code)
	require.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))

	_, code = do("Bearer pg_test_wrong")
	require.Equal(t, 401, code)
	_, code = do("Bearer pg_test_wrong")
	require.Equal(t, 401, code)
	require.EqualValues(t, 1, f.calls.Load(), "unknown key is negatively cached")

	_, code = do("Bearer " + key)
	require.Equal(t, 200, code)
	_, code = do("Bearer " + key)
	require.Equal(t, 200, code)
	require.EqualValues(t, 2, f.calls.Load(), "valid key is cached")
	require.Equal(t, "acme", got.TenantName)
	require.Equal(t, 5, got.Tenant.RPM)
	require.Equal(t, []string{"default"}, got.AllowedRoutes)
}

func TestBearerCaseInsensitiveAndWWWAuthenticate(t *testing.T) {
	key, _, hash, _ := GenerateKey("test")
	f := &fakeLookup{keys: map[string]store.KeyRecord{string(hash): {
		Key:    store.APIKey{ID: "k1", Name: "ci"},
		Tenant: store.Tenant{ID: "t1", Name: "acme"},
	}}}
	mw := NewMiddleware(f, time.Minute, time.Minute)
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	cases := []string{
		"Bearer " + key,
		"bearer " + key,
		"BEARER " + key,
		"Bearer   " + key + "  ",
	}
	for _, authHeader := range cases {
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		req.Header.Set("Authorization", authHeader)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code, "scheme casing %q should be accepted", authHeader)
	}

	// 401 must include WWW-Authenticate: Bearer
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, 401, rec.Code)
	require.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
}

func TestSingleflightConcurrentMisses(t *testing.T) {
	f := &fakeLookup{
		delay: 20 * time.Millisecond,
		keys:  map[string]store.KeyRecord{},
	}
	mw := NewMiddleware(f, time.Minute, time.Minute)
	// Give generous rate limit for test
	mw.SetFailedAuthLimiter(rate.Inf, 1000)

	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	var wg sync.WaitGroup
	const n = 100
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			req.Header.Set("Authorization", "Bearer pg_same_unknown_key")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			require.Equal(t, 401, rec.Code)
		}()
	}
	wg.Wait()

	require.EqualValues(t, 1, f.calls.Load(), "100 concurrent requests for same key must coalesce to 1 DB call via singleflight")
}

func TestRandomInvalidKeysDoNotEvictValidCache(t *testing.T) {
	key, _, hash, _ := GenerateKey("test")
	f := &fakeLookup{keys: map[string]store.KeyRecord{string(hash): {
		Key:    store.APIKey{ID: "k1", Name: "ci"},
		Tenant: store.Tenant{ID: "t1", Name: "acme"},
	}}}
	mw := NewMiddleware(f, time.Minute, time.Minute)
	// Relax per-IP limiter for this test
	mw.SetFailedAuthLimiter(rate.Inf, 200_000)

	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// 1. Warm the valid key into cache
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	require.EqualValues(t, 1, f.calls.Load(), "initial DB call to load valid key")

	// 2. Flood with 100,000 distinct random invalid keys
	const floodCount = 100_000
	for i := 0; i < floodCount; i++ {
		// Use resolve directly to simulate 100k random keys
		_, ok, err := mw.resolve(context.Background(), fmt.Sprintf("pg_random_bad_key_%d", i))
		require.NoError(t, err)
		require.False(t, ok)
	}

	// 3. Request valid key again -> must be a cache hit (0 additional DB calls)
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req2.Header.Set("Authorization", "Bearer "+key)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	require.Equal(t, 200, rec2.Code)
	require.EqualValues(t, 1+floodCount, f.calls.Load())

	// 4. Request valid key one more time -> still cached!
	req3 := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req3.Header.Set("Authorization", "Bearer "+key)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	require.Equal(t, 200, rec3.Code)
	require.EqualValues(t, 1+floodCount, f.calls.Load(), "valid key was not evicted by 100k invalid keys")
}

func TestPerIPFailedAuthLimiter(t *testing.T) {
	f := &fakeLookup{keys: map[string]store.KeyRecord{}}
	mw := NewMiddleware(f, time.Minute, time.Minute)
	// Set burst to 5 failed attempts
	mw.SetFailedAuthLimiter(rate.Every(time.Minute), 5)

	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	doReq := func(ip, key string) int {
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		req.RemoteAddr = ip + ":12345"
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// IP 1: 5 failed attempts -> 401
	for i := 1; i <= 5; i++ {
		require.Equal(t, 401, doReq("192.0.2.1", fmt.Sprintf("pg_bad_%d", i)), "attempt %d should be 401", i)
	}

	// IP 1: 6th and 7th attempt -> 429 Too Many Requests
	require.Equal(t, 429, doReq("192.0.2.1", "pg_bad_6"), "attempt 6 should be rate-limited with 429")
	require.Equal(t, 429, doReq("192.0.2.1", "pg_bad_7"), "attempt 7 should be rate-limited with 429")

	// IP 2: independent IP limit, should still be admitted for up to 5 attempts
	require.Equal(t, 401, doReq("192.0.2.2", "pg_bad_ip2_1"))
}
