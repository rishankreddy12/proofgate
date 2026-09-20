package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/stretchr/testify/require"
)

func TestMetricsStage(t *testing.T) {
	m := NewMetrics()
	c := pipeline.NewCall(auth.Principal{}, &api.ChatRequest{}, &router.Route{Name: "default"})
	c.Target = router.Target{Provider: "a", Model: "small"}
	c.Usage = api.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	c.CostMicros = 2_000_000
	c.Latency, c.UpstreamTime = 100*time.Millisecond, 97*time.Millisecond
	m.Stage().After(context.Background(), c)

	require.Equal(t, 1.0, testutil.ToFloat64(m.requests.WithLabelValues("default", "a/small", "ok", "miss", "false")))
	require.Equal(t, 10.0, testutil.ToFloat64(m.tokens.WithLabelValues("default", "a/small", "prompt")))
	require.Equal(t, 2.0, testutil.ToFloat64(m.cost.WithLabelValues("default", "a/small")))
	require.Equal(t, 1, testutil.CollectAndCount(m.overhead))
}

func TestStatusOf(t *testing.T) {
	require.Equal(t, "ok", StatusOf(nil))
	require.Equal(t, "rate_limited", StatusOf(api.RateLimited("rate_limited", 0)))
	require.Equal(t, "client_closed", StatusOf(context.Canceled))
	require.Equal(t, "error", StatusOf(errors.New("x")))
}

func TestAccessLogKeepsFlusherAndHidesSecrets(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(old)

	h := AccessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, ok := w.(http.Flusher)
		require.True(t, ok, "SSE needs Flush through the access log wrapper")
		w.Header().Set("X-ProofGate-Request-Id", "rid-1")
		w.WriteHeader(201)
		w.(http.Flusher).Flush()
	}))
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"secret":"prompt"}`))
	req.Header.Set("Authorization", "Bearer pg_live_topsecret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.True(t, rec.Flushed)
	line := buf.String()
	require.Contains(t, line, `"status":201`)
	require.Contains(t, line, `"request_id":"rid-1"`)
	require.NotContains(t, line, "topsecret")
	require.NotContains(t, line, "prompt")
}

func TestTracingNoopWithoutEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := SetupTracing(context.Background(), "proofgate")
	require.NoError(t, err)
	require.NoError(t, shutdown(context.Background()))
}
