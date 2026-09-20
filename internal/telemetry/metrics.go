// Package telemetry provides Prometheus metrics, OpenTelemetry tracing and access logs.
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

var latencyBuckets = []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120}

type Metrics struct {
	Registry   *prometheus.Registry
	FailOpen   prometheus.Counter
	requests   *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	overhead   *prometheus.HistogramVec
	ttft       *prometheus.HistogramVec
	tokens     *prometheus.CounterVec
	cost       *prometheus.CounterVec
	unpriced   *prometheus.CounterVec
	breaker    *prometheus.GaugeVec
	embeds     *prometheus.CounterVec
	embedToks  *prometheus.CounterVec
}

func NewMetrics() *Metrics {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	f := func(c prometheus.Collector) { r.MustRegister(c) }
	m := &Metrics{Registry: r,
		FailOpen: prometheus.NewCounter(prometheus.CounterOpts{Name: "proofgate_ratelimit_fail_open_total", Help: "Requests allowed because Redis was unavailable."}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_requests_total", Help: "Chat requests."}, []string{"route", "target", "status", "cache", "stream"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "proofgate_request_duration_seconds", Help: "End-to-end latency.", Buckets: latencyBuckets}, []string{"route", "stream"}),
		overhead: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "proofgate_overhead_seconds", Help: "Latency minus time spent waiting on providers.", Buckets: latencyBuckets}, []string{"stream"}),
		ttft:     prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "proofgate_ttft_seconds", Help: "Time to first token for streams.", Buckets: latencyBuckets}, []string{"route"}),
		tokens:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_tokens_total", Help: "Tokens by kind."}, []string{"route", "target", "kind"}),
		cost:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_cost_usd_total", Help: "Spend in USD."}, []string{"route", "target"}),
		unpriced: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_unpriced_requests_total", Help: "Calls to targets with no price configured."}, []string{"target"}),
		breaker:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "proofgate_breaker_open", Help: "1 open, 0.5 half-open, 0 closed."}, []string{"target"}),
		embeds:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_embeddings_total", Help: "Embedding requests."}, []string{"route", "target", "status"}),
		embedToks: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "proofgate_embedding_tokens_total", Help: "Embedding tokens."}, []string{"route", "target", "status"}),
	}
	for _, c := range []prometheus.Collector{m.FailOpen, m.requests, m.duration, m.overhead, m.ttft, m.tokens, m.cost, m.unpriced, m.breaker, m.embeds, m.embedToks} {
		f(c)
	}
	return m
}

// Counter registers (once) and returns a labelled counter.
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

func (m *Metrics) ObserveEmbed(route, target, status string, tokens int, costMicros int64, d time.Duration) {
	m.embeds.WithLabelValues(route, target, status).Inc()
	m.embedToks.WithLabelValues(route, target, status).Add(float64(tokens))
	m.cost.WithLabelValues(route, target).Add(float64(costMicros) / 1e6)
}

type metricsStage struct{ m *Metrics }

func (m *Metrics) Stage() pipeline.Stage { return metricsStage{m} }

func (s metricsStage) Name() string                                        { return "metrics" }
func (s metricsStage) Before(context.Context, *pipeline.Call) (bool, error) { return false, nil }

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
	}
}
