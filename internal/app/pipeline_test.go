package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/proofgate/proofgate/internal/agentrun"
	"github.com/proofgate/proofgate/internal/analytics"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/app"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/cache"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/proofgate/proofgate/internal/ratelimit"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/proofgate/proofgate/internal/server"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/proofgate/proofgate/internal/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStageOrder asserts the canonical pipeline stage ordering.
// Any reordering must be a deliberate, reviewed change.
func TestStageOrder(t *testing.T) {
	d := app.Deps{
		Metrics:        telemetry.NewMetrics(),
		UsageEmit:      func(_ analytics.UsageEvent) bool { return true },
		Runs:           noopRunStore{},
		FuzzyDetector:  nil,
		DefaultMaxToks: 4096,
		CacheStage:     cache.NewStage(nil, nil, nil, nil, nil),
		Limiter:        noopLimiter{},
		MaxTokReserve:  1000,
		FailOpenInc:    func() {},
		Ledger:         noopLedger{},
	}
	pipe := app.BuildPipeline(d)
	got := pipe.Names()

	require.Len(t, got, len(app.StageOrder), "pipeline stage count changed — update StageOrder")
	assert.Equal(t, app.StageOrder, got, "pipeline stage order has changed — this must be a deliberate, reviewed change")
}

// TestPipelineEndToEnd serves a real Chat request through the production pipeline order
// and an in-memory mockllm backend via httptest, verifying the full stage lifecycle.
func TestPipelineEndToEnd(t *testing.T) {
	mockUpstream := mockllm.New("upstream-1", mockllm.Mode{Reply: "hello from mockllm"})
	mockSrv := httptest.NewServer(mockUpstream.Handler())
	t.Cleanup(mockSrv.Close)

	cfgYAML := fmt.Sprintf(`
providers:
  - name: mock-prov
    type: openai
    base_url: %q
pricing:
  mock-prov/gpt-4o: {input: 1, output: 2}
routes:
  - name: default
    retry: {max_attempts: 1, base_delay: 1ms}
    targets: [{provider: mock-prov, model: gpt-4o}]
`, mockSrv.URL+"/v1")

	cfg, err := config.Parse([]byte(cfgYAML))
	require.NoError(t, err)

	br := router.NewBreakers(5, time.Minute, time.Now)
	rt, err := server.BuildRuntime(cfg, br, os.Getenv, nil)
	require.NoError(t, err)

	st := &server.State{}
	st.Store(rt)

	var usageEvents []analytics.UsageEvent
	metrics := telemetry.NewMetrics()

	d := app.Deps{
		Metrics: metrics,
		UsageEmit: func(ev analytics.UsageEvent) bool {
			usageEvents = append(usageEvents, ev)
			return true
		},
		Runs:           noopRunStore{},
		FuzzyDetector:  nil,
		DefaultMaxToks: 4096,
		CacheStage:     cache.NewStage(nil, nil, nil, nil, nil),
		Limiter:        noopLimiter{},
		MaxTokReserve:  1000,
		FailOpenInc:    func() {},
		Ledger:         noopLedger{},
	}

	pipe := app.BuildPipeline(d)

	h := &server.Handlers{
		State:    st,
		Breakers: br,
		Pipeline: pipe,
		Limiter:  d.Limiter,
		Ledger:   d.Ledger,
		Now:      time.Now,
		Metrics:  metrics,
	}

	principal := auth.Principal{
		TenantID:      "t1",
		TenantName:    "acme",
		AllowedRoutes: []string{"default"},
		Tenant:        store.TenantPolicy{RPM: 100, TPM: 100000},
	}
	fakeAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}

	gw := httptest.NewServer(h.Routes(fakeAuth))
	t.Cleanup(gw.Close)

	reqBody := api.ChatRequest{
		Model: "default",
		Messages: []api.Message{
			{Role: "user", Content: api.Content{Text: "ping hello"}},
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
	require.NoError(t, err)

	resp, err := http.Post(gw.URL+"/v1/chat/completions", "application/json", bytes.NewReader(bodyBytes))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var chatResp api.ChatResponse
	err = json.NewDecoder(resp.Body).Decode(&chatResp)
	require.NoError(t, err)
	require.NotEmpty(t, chatResp.Choices)
	assert.Contains(t, chatResp.Choices[0].Message.Content.Text, "hello from mockllm")

	// Verify telemetry/analytics ran through the pipeline hooks
	assert.NotEmpty(t, usageEvents, "usage analytics stage should have emitted an event")
}

// --- Minimal no-op implementations satisfying stage interfaces for harness ---

type noopRunStore struct{}

func (noopRunStore) Step(_ context.Context, _, _ string, _ store.RunPolicy, _ string, _ int64, _ int) (agentrun.StepResult, error) {
	return agentrun.StepResult{}, nil
}
func (noopRunStore) Charge(_ context.Context, _, _ string, _ store.RunPolicy, _ int64, _ int, _ int64, _ int) error {
	return nil
}

type noopLimiter struct{}

func (noopLimiter) Take(_ context.Context, _ string, _ store.TenantPolicy, _ int) (ratelimit.Decision, error) {
	return ratelimit.Decision{Allowed: true}, nil
}
func (noopLimiter) Adjust(_ context.Context, _ string, _ store.TenantPolicy, _ int) error {
	return nil
}

type noopLedger struct{}

func (noopLedger) Spent(_ context.Context, _, _ string) (int64, error) { return 0, nil }
func (noopLedger) Add(_ context.Context, _, _ string, _ int64) error   { return nil }
func (noopLedger) Reserve(_ context.Context, _, _ string, est, _ int64) (bool, int64, error) {
	return true, est, nil
}

// TestSpoofedInternalHeaderCannotBypassRateLimiter tests C1: an external client sending
// X-ProofGate-Internal: true cannot bypass rate limiting, and causes the spoof detection metric to increment.
// Meanwhile, trusted internal calls (via ChatInternal) set c.Internal=true and successfully bypass the limiter.
func TestSpoofedInternalHeaderCannotBypassRateLimiter(t *testing.T) {
	mockUpstream := mockllm.New("upstream-1", mockllm.Mode{Reply: "ok"})
	mockSrv := httptest.NewServer(mockUpstream.Handler())
	t.Cleanup(mockSrv.Close)

	cfgYAML := fmt.Sprintf(`
providers:
  - name: mock-prov
    type: openai
    base_url: %q
pricing:
  mock-prov/gpt-4o: {input: 1, output: 2}
routes:
  - name: default
    retry: {max_attempts: 1, base_delay: 1ms}
    targets: [{provider: mock-prov, model: gpt-4o}]
`, mockSrv.URL+"/v1")

	cfg, err := config.Parse([]byte(cfgYAML))
	require.NoError(t, err)

	br := router.NewBreakers(5, time.Minute, time.Now)
	rt, err := server.BuildRuntime(cfg, br, os.Getenv, nil)
	require.NoError(t, err)

	st := &server.State{}
	st.Store(rt)

	metrics := telemetry.NewMetrics()
	limiter := &countLimiter{max: 1}

	d := app.Deps{
		Metrics:        metrics,
		UsageEmit:      func(_ analytics.UsageEvent) bool { return true },
		Runs:           noopRunStore{},
		FuzzyDetector:  nil,
		DefaultMaxToks: 4096,
		CacheStage:     cache.NewStage(nil, nil, nil, nil, nil),
		Limiter:        limiter,
		MaxTokReserve:  1000,
		FailOpenInc:    func() {},
		Ledger:         noopLedger{},
	}

	pipe := app.BuildPipeline(d)

	h := &server.Handlers{
		State:    st,
		Breakers: br,
		Pipeline: pipe,
		Limiter:  limiter,
		Ledger:   d.Ledger,
		Now:      time.Now,
		Metrics:  metrics,
	}

	principal := auth.Principal{
		TenantID:      "tenant-alpha",
		TenantName:    "alpha",
		AllowedRoutes: []string{"default"},
		Tenant:        store.TenantPolicy{RPM: 1, Strict: true},
	}
	fakeAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}

	gw := httptest.NewServer(h.Routes(fakeAuth))
	t.Cleanup(gw.Close)

	postWithHeader := func(headerVal string) *http.Response {
		reqBody := api.ChatRequest{
			Model:    "default",
			Messages: []api.Message{{Role: "user", Content: api.Content{Text: "test"}}},
		}
		bodyBytes, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", gw.URL+"/v1/chat/completions", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		if headerVal != "" {
			req.Header.Set("X-ProofGate-Internal", headerVal)
		}
		resp, postErr := http.DefaultClient.Do(req)
		require.NoError(t, postErr)
		return resp
	}

	// 1st request with spoofed internal header: allowed (count = 1 <= max 1)
	resp1 := postWithHeader("true")
	defer resp1.Body.Close()
	require.Equal(t, http.StatusOK, resp1.StatusCode)

	// 2nd request with spoofed internal header: MUST be rate limited (429)!
	// If the spoof had worked, it would have returned 200.
	resp2 := postWithHeader("true")
	defer resp2.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, resp2.StatusCode)
	require.Equal(t, "5", resp2.Header.Get("Retry-After"))

	// Check spoof detection metric: must have observed 2 spoof attempts for tenant-alpha
	spoofCount := testutil.ToFloat64(metrics.SpoofedInternal.WithLabelValues("tenant-alpha"))
	require.Equal(t, float64(2), spoofCount, "should record both spoofing attempts")

	// Trusted internal call via ChatInternal: MUST bypass rate limiter even when quota exhausted
	intResp, intErr := h.ChatInternal(context.Background(), "default", &api.ChatRequest{
		Messages: []api.Message{{Role: "user", Content: api.Content{Text: "internal test"}}},
	})
	require.NoError(t, intErr, "ChatInternal must bypass rate limiting")
	require.NotNil(t, intResp)
}

type countLimiter struct {
	mu    sync.Mutex
	count int
	max   int
}

func (l *countLimiter) Take(_ context.Context, _ string, _ store.TenantPolicy, _ int) (ratelimit.Decision, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.count++
	if l.count > l.max {
		return ratelimit.Decision{Allowed: false, RetryAfter: 5 * time.Second}, nil
	}
	return ratelimit.Decision{Allowed: true}, nil
}

func (l *countLimiter) Adjust(_ context.Context, _ string, _ store.TenantPolicy, _ int) error {
	return nil
}

type memExactCache struct {
	mu      sync.RWMutex
	entries map[string]cache.Entry
}

func newMemExactCache() *memExactCache {
	return &memExactCache{entries: make(map[string]cache.Entry)}
}

func (m *memExactCache) Get(_ context.Context, key string) (*cache.Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[key]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (m *memExactCache) Put(_ context.Context, _, key string, e cache.Entry, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[key] = e
	return nil
}

// TestPIIRestoreOrderingAndCacheRace verifies C2, C3, and C4:
// 1. Unary non-streaming chat with PII redaction restores PII in the client response.
// 2. Canonical c.Response retains placeholders and is cached in placeholder form (no PII in cache).
// 3. Cache hits for different users restore using the current caller's PII mapping (no cross-user PII leak).
// 4. Concurrent requests with -race run safely without data races.
func TestPIIRestoreOrderingAndCacheRace(t *testing.T) {
	mockUpstream := mockllm.New("upstream-1", mockllm.Mode{Reply: "Confirmed: <EMAIL_1> has been registered."})
	mockSrv := httptest.NewServer(mockUpstream.Handler())
	t.Cleanup(mockSrv.Close)

	cfgYAML := fmt.Sprintf(`
providers:
  - name: mock-prov
    type: openai
    base_url: %q
pricing:
  mock-prov/gpt-4o: {input: 1, output: 2}
routes:
  - name: default
    guard:
      pii: {enabled: true, mode: redact}
    cache:
      mode: on
      exact: true
      ttl: 1h
      max_entry_bytes: 65536
    retry: {max_attempts: 1, base_delay: 1ms}
    targets: [{provider: mock-prov, model: gpt-4o}]
`, mockSrv.URL+"/v1")

	cfg, err := config.Parse([]byte(cfgYAML))
	require.NoError(t, err)

	br := router.NewBreakers(5, time.Minute, time.Now)
	rt, err := server.BuildRuntime(cfg, br, os.Getenv, nil)
	require.NoError(t, err)

	st := &server.State{}
	st.Store(rt)

	exactCache := newMemExactCache()
	cacheStage := cache.NewStage(exactCache, nil, nil, nil, nil)

	d := app.Deps{
		Metrics:        telemetry.NewMetrics(),
		UsageEmit:      func(_ analytics.UsageEvent) bool { return true },
		Runs:           noopRunStore{},
		FuzzyDetector:  nil,
		DefaultMaxToks: 4096,
		CacheStage:     cacheStage,
		Limiter:        noopLimiter{},
		MaxTokReserve:  1000,
		FailOpenInc:    func() {},
		Ledger:         noopLedger{},
	}

	pipe := app.BuildPipeline(d)

	h := &server.Handlers{
		State:    st,
		Breakers: br,
		Pipeline: pipe,
		Limiter:  d.Limiter,
		Ledger:   d.Ledger,
		Now:      time.Now,
		Metrics:  d.Metrics,
	}

	principal := auth.Principal{
		TenantID:      "tenant-pii",
		TenantName:    "pii-corp",
		AllowedRoutes: []string{"default"},
	}
	fakeAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}

	gw := httptest.NewServer(h.Routes(fakeAuth))
	t.Cleanup(gw.Close)

	doChat := func(prompt string) string {
		reqBody := api.ChatRequest{
			Model:    "default",
			Messages: []api.Message{{Role: "user", Content: api.Content{Text: prompt}}},
		}
		bodyBytes, _ := json.Marshal(reqBody)
		resp, postErr := http.Post(gw.URL+"/v1/chat/completions", "application/json", bytes.NewReader(bodyBytes))
		require.NoError(t, postErr)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var chatResp api.ChatResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&chatResp))
		require.NotEmpty(t, chatResp.Choices)
		return chatResp.Choices[0].Message.Content.Text
	}

	// 1. User A sends prompt with alice@example.com (Cache Miss)
	replyA := doChat("Register account for alice@example.com now.")
	require.Equal(t, "Confirmed: alice@example.com has been registered.", replyA, "User A must receive their restored email")

	// Wait for async cache write
	cacheStage.Wait()

	// Verify raw cache entry stored in exactCache: MUST contain placeholder, NEVER raw email
	exactCache.mu.RLock()
	require.Len(t, exactCache.entries, 1)
	for _, entry := range exactCache.entries {
		require.Contains(t, entry.Response.Choices[0].Message.Content.Text, "<EMAIL_1>", "cache entry must store placeholders")
		require.NotContains(t, entry.Response.Choices[0].Message.Content.Text, "alice@example.com", "cache entry must NOT store PII")
	}
	exactCache.mu.RUnlock()

	// 2. User B sends identical prompt structure with bob@example.com (Cache Hit!)
	replyB := doChat("Register account for bob@example.com now.")
	require.Equal(t, "Confirmed: bob@example.com has been registered.", replyB, "User B must receive their OWN restored email on cache hit, not Alice's")

	// 3. Concurrency check: multiple concurrent callers to verify race freedom
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			email := fmt.Sprintf("user%d@example.com", idx)
			res := doChat(fmt.Sprintf("Register account for %s now.", email))
			expected := fmt.Sprintf("Confirmed: %s has been registered.", email)
			require.Equal(t, expected, res)
		}(i)
	}
	wg.Wait()
}
