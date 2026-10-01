package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

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
