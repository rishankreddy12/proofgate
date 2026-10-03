// Package telemetry provides enterprise-grade capabilities, configuration, and structural components for the telemetry subsystem.
package telemetry

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/pipeline"
)

// latencyBuckets define the histogram bucket boundaries (in seconds) for measuring
// both end-to-end Gateway latency and Time-To-First-Token (TTFT) for streaming responses.
var latencyBuckets = []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120}

// Metrics encapsulates the global Prometheus registry and all custom ProofGate
// instrumentation collectors (Counters, Gauges, Histograms).
type Metrics struct {
	Registry           *prometheus.Registry
	FailOpen           prometheus.Counter
	BudgetFailOpen     prometheus.Counter
	SpoofedInternal    *prometheus.CounterVec
	UnpricedCalls      *prometheus.CounterVec
	AdminAuditFailures prometheus.Counter
	requests           *prometheus.CounterVec
	duration           *prometheus.HistogramVec
	overhead           *prometheus.HistogramVec
	ttft               *prometheus.HistogramVec
	tokens             *prometheus.CounterVec
	cost               *prometheus.CounterVec
	unpriced           *prometheus.CounterVec
	breaker            *prometheus.GaugeVec
	embeds             *prometheus.CounterVec
	embedToks          *prometheus.CounterVec
	hedges             *prometheus.CounterVec
	hedgeWasted        *prometheus.CounterVec
}

// NewMetrics initializes the Prometheus registry, wires up default Go runtime/process
// collectors, and instantiates all custom ProofGate telemetry vectors.
func NewMetrics() *Metrics {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	f := func(c prometheus.Collector) { r.MustRegister(c) }

	m := &Metrics{Registry: r,
		FailOpen:           prometheus.NewCounter(prometheus.CounterOpts{Name: "proofgate_ratelimit_fail_open_total", Help: "Requests allowed because Redis was unavailable."}),
		BudgetFailOpen:     prometheus.NewCounter(prometheus.CounterOpts{Name: "proofgate_budget_fail_open_total", Help: "Requests allowed because budget backend was unavailable."}),
		SpoofedInternal:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_spoofed_internal_header_total", Help: "Requests where client supplied untrusted X-ProofGate-Internal header."}, []string{"tenant"}),
		UnpricedCalls:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_unpriced_calls_total", Help: "Calls to targets with no price configured."}, []string{"provider", "model"}),
		AdminAuditFailures: prometheus.NewCounter(prometheus.CounterOpts{Name: "proofgate_admin_audit_failures_total", Help: "Privileged admin audit log write failures."}),
		requests:           prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_requests_total", Help: "Chat requests."}, []string{"route", "target", "status", "cache", "stream"}),
		duration:           prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "proofgate_request_duration_seconds", Help: "End-to-end latency.", Buckets: latencyBuckets}, []string{"route", "stream"}),
		overhead:           prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "proofgate_overhead_seconds", Help: "Latency minus time spent waiting on providers.", Buckets: latencyBuckets}, []string{"stream"}),
		ttft:               prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "proofgate_ttft_seconds", Help: "Time to first token for streams.", Buckets: latencyBuckets}, []string{"route"}),
		tokens:             prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_tokens_total", Help: "Tokens by kind."}, []string{"route", "target", "kind"}),
		cost:               prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_cost_usd_total", Help: "Spend in USD."}, []string{"route", "target"}),
		unpriced:           prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_unpriced_requests_total", Help: "Calls to targets with no price configured."}, []string{"target"}),
		breaker:            prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "proofgate_breaker_open", Help: "1 open, 0.5 half-open, 0 closed."}, []string{"target"}),
		embeds:             prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_embeddings_total", Help: "Embedding requests."}, []string{"route", "target", "status"}),
		embedToks:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_embedding_tokens_total", Help: "Embedding tokens."}, []string{"route", "target", "status"}),
		hedges:             prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_hedges_total", Help: "Hedged requests."}, []string{"route", "outcome"}),
		hedgeWasted:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_hedge_wasted_tokens_total", Help: "Tokens consumed by cancelled hedge losers."}, []string{"route", "target"}),
	}
	for _, c := range []prometheus.Collector{m.FailOpen, m.BudgetFailOpen, m.SpoofedInternal, m.UnpricedCalls, m.AdminAuditFailures, m.requests, m.duration, m.overhead, m.ttft, m.tokens, m.cost, m.unpriced, m.breaker, m.embeds, m.embedToks, m.hedges, m.hedgeWasted} {
		f(c)
	}
	return m
}

// Counter registers a new labelled counter safely, returning the existing one if it was already registered.
func (m *Metrics) Counter(name, help string, labels ...string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	if err := m.Registry.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if errors.As(err, &are) {
			return are.ExistingCollector.(*prometheus.CounterVec)
		}
		panic(err)
	}
	return c
}

// StatusOf normalizes arbitrary Go errors into high-level categorical strings (e.g., "ok", "client_closed",
// "rate_limit_exceeded") to prevent Prometheus label cardinality explosion.
func StatusOf(err error) string {
	if err == nil {
		return "ok"
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	if errors.Is(err, context.Canceled) {
		return "client_closed"
	}
	return "error"
}

// SetBreaker records the real-time state of the Circuit Breaker for a specific upstream target.
func (m *Metrics) SetBreaker(target, state string) {
	v := 0.0
	switch state {
	case "open":
		v = 1
	case "half-open":
		v = 0.5
	}
	m.breaker.WithLabelValues(target).Set(v)
}

// ObserveEmbed logs telemetry specific to `/v1/embeddings` endpoints.
func (m *Metrics) ObserveEmbed(route, target, status string, tokens int, costMicros int64, d time.Duration) {
	m.embeds.WithLabelValues(route, target, status).Inc()
	m.embedToks.WithLabelValues(route, target, status).Add(float64(tokens))
	m.cost.WithLabelValues(route, target).Add(float64(costMicros) / 1e6)
}

// ObserveHedge increments the counter for tail-latency hedged requests (whether the hedge won or lost).
func (m *Metrics) ObserveHedge(route, outcome string) {
	if m == nil || m.hedges == nil {
		return
	}
	m.hedges.WithLabelValues(route, outcome).Inc()
}

// ObserveHedgeWaste records tokens consumed by the losing (cancelled) hedge request.
// This is critical for auditing the financial overhead of enabling Hedging on a route.
func (m *Metrics) ObserveHedgeWaste(route, target string, tokens int) {
	if m == nil || m.hedgeWasted == nil || tokens <= 0 {
		return
	}
	m.hedgeWasted.WithLabelValues(route, target).Add(float64(tokens))
}

// ObserveSpoofedInternal audits when an external client attempts to smuggle internal API headers.
func (m *Metrics) ObserveSpoofedInternal(tenant string) {
	if m == nil || m.SpoofedInternal == nil {
		return
	}
	if tenant == "" {
		tenant = "unknown"
	}
	m.SpoofedInternal.WithLabelValues(tenant).Inc()
}

// ObserveUnpricedCall tracks invocations to models that lack cost configurations in the pricing matrix.
func (m *Metrics) ObserveUnpricedCall(provider, model string) {
	if m == nil || m.UnpricedCalls == nil {
		return
	}
	if provider == "" {
		provider = "unknown"
	}
	if model == "" {
		model = "unknown"
	}
	m.UnpricedCalls.WithLabelValues(provider, model).Inc()
}

type metricsStage struct{ m *Metrics }

// Stage returns the Metrics pipeline plugin that automatically observes Request Lifecycles.
func (m *Metrics) Stage() pipeline.Stage { return metricsStage{m} }

// Name identifies the pipeline stage.
func (s metricsStage) Name() string { return "metrics" }

// Before executes prior to routing, currently a no-op for the metrics stage.
func (s metricsStage) Before(context.Context, *pipeline.Call) (bool, error) { return false, nil }

// After executes exactly once at the end of every Chat Completion request.
// It extracts the final disposition (Tokens, Cost, Route, Target, Latency) and increments
// the respective Prometheus vectors.
func (s metricsStage) After(_ context.Context, c *pipeline.Call) {
	route, target := "", c.Target.String()
	if c.Route != nil {
		route = c.Route.Name
	}
	if c.Target.Provider == "" {
		target = "none"
	}
	stream := strconv.FormatBool(c.Stream)
	latency := c.Latency
	if latency == 0 {
		latency = time.Since(c.Start)
	}

	s.m.requests.WithLabelValues(route, target, StatusOf(c.Err), c.CacheStatus, stream).Inc()
	s.m.duration.WithLabelValues(route, stream).Observe(latency.Seconds())
	if c.Target.Provider != "" {
		s.m.overhead.WithLabelValues(stream).Observe(max(latency-c.UpstreamTime, 0).Seconds())
	}
	if c.Stream && c.TTFT > 0 {
		s.m.ttft.WithLabelValues(route).Observe(c.TTFT.Seconds())
	}

	s.m.tokens.WithLabelValues(route, target, "prompt").Add(float64(c.Usage.PromptTokens))
	s.m.tokens.WithLabelValues(route, target, "completion").Add(float64(c.Usage.CompletionTokens))
	s.m.tokens.WithLabelValues(route, target, "cached").Add(float64(c.Usage.CachedTokens()))
	s.m.cost.WithLabelValues(route, target).Add(float64(c.CostMicros) / 1e6)

	if _, ok := c.Values["cost.unpriced"]; ok {
		s.m.unpriced.WithLabelValues(target).Inc()
		s.m.ObserveUnpricedCall(c.Target.Provider, c.Target.Model)
	}
}
