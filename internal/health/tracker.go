// Package health tracks per-target latency, throughput and errors, and marks targets that breach their SLO.
package health

import (
	"math"
	"strings"
	"sync"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/router"
)

type Outcome int

const (
	Unknown Outcome = iota
	OK
	Failed
)

type Sample struct {
	Target  router.Target
	TTFT    time.Duration
	Tokens  int
	Gen     time.Duration
	Outcome Outcome
}

type Stats struct {
	TTFTMs    float64   `json:"ttft_ms"`
	TTFTDevMs float64   `json:"ttft_dev_ms"`
	TPS       float64   `json:"tokens_per_sec"`
	ErrRate   float64   `json:"error_rate"`
	TTFTN     int       `json:"ttft_n"`
	TPSN      int       `json:"tps_n"`
	ErrN      int       `json:"err_n"`
	Degraded  bool      `json:"degraded"`
	Since     time.Time `json:"since"`
	Reason    string    `json:"reason"`
}

type entry struct {
	Stats
	breaches   int
	lastBreach time.Time
}

type Tracker struct {
	mu           sync.Mutex
	cfg          config.HealthConfig
	slos         map[string]config.SLO
	now          func() time.Time
	m            map[router.Target]*entry
	knownTargets map[router.Target]bool
}

func NewTracker(cfg config.HealthConfig, slos map[string]config.SLO, now func() time.Time) *Tracker {
	return &Tracker{
		cfg:          cfg,
		slos:         slos,
		now:          now,
		m:            map[router.Target]*entry{},
		knownTargets: map[router.Target]bool{},
	}
}

// SetTargets restricts the tracker to only allow peer merges for the given targets.
func (t *Tracker) SetTargets(targets []router.Target) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.knownTargets = make(map[router.Target]bool, len(targets))
	for _, tg := range targets {
		t.knownTargets[tg] = true
	}
}

func (t *Tracker) SetSLOs(s map[string]config.SLO) {
	t.mu.Lock()
	t.slos = s
	t.mu.Unlock()
}

func (t *Tracker) get(tg router.Target) *entry {
	e, ok := t.m[tg]
	if !ok {
		e = &entry{}
		t.m[tg] = e
	}
	return e
}

func ewma(old, x, a float64, n int) float64 {
	if n == 0 {
		return x
	}
	return (1-a)*old + a*x
}

func (t *Tracker) Observe(s Sample) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, a := t.get(s.Target), t.cfg.Alpha
	if s.TTFT > 0 {
		x := float64(s.TTFT.Microseconds()) / 1000
		if e.TTFTN == 0 {
			e.TTFTMs, e.TTFTDevMs = x, 0
		} else {
			e.TTFTDevMs = (1-a)*e.TTFTDevMs + a*math.Abs(x-e.TTFTMs)
			e.TTFTMs = (1-a)*e.TTFTMs + a*x
		}
		e.TTFTN++
	}
	if s.Tokens > 0 && s.Gen > 0 {
		e.TPS = ewma(e.TPS, float64(s.Tokens)/s.Gen.Seconds(), a, e.TPSN)
		e.TPSN++
	}
	if s.Outcome != Unknown {
		x := 0.0
		if s.Outcome == Failed {
			x = 1
		}
		e.ErrRate = ewma(e.ErrRate, x, a, e.ErrN)
		e.ErrN++
	}
	t.evaluate(s.Target, e)
}

// Merge blends peer stats from cross-replica gossip or Redis into the local tracker's state
// using a damped alpha (0.1) to avoid sudden oscillations while rapidly spreading
// upstream degradation awareness across replicas.
func (t *Tracker) Merge(tg router.Target, peer Stats) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Drop stats for unknown targets if a target allowlist is configured
	if len(t.knownTargets) > 0 && !t.knownTargets[tg] {
		return
	}

	// Validate floats: reject NaN and Inf
	if math.IsNaN(peer.TTFTMs) || math.IsInf(peer.TTFTMs, 0) ||
		math.IsNaN(peer.TTFTDevMs) || math.IsInf(peer.TTFTDevMs, 0) ||
		math.IsNaN(peer.TPS) || math.IsInf(peer.TPS, 0) ||
		math.IsNaN(peer.ErrRate) || math.IsInf(peer.ErrRate, 0) {
		return
	}

	// Validate bounds: reject negative metrics or absurd sample counts
	const maxCount = 1_000_000
	if peer.TTFTMs < 0 || peer.TTFTDevMs < 0 || peer.TPS < 0 || peer.ErrRate < 0 ||
		peer.TTFTN < 0 || peer.TPSN < 0 || peer.ErrN < 0 ||
		peer.TTFTN > maxCount || peer.TPSN > maxCount || peer.ErrN > maxCount {
		return
	}

	// Clamp error rate to [0, 1]
	if peer.ErrRate > 1.0 {
		peer.ErrRate = 1.0
	}

	e := t.get(tg)
	const gossipAlpha = 0.1

	if peer.TTFTN > 0 {
		if e.TTFTN == 0 {
			e.TTFTMs = peer.TTFTMs
			e.TTFTDevMs = peer.TTFTDevMs
			e.TTFTN = peer.TTFTN
		} else {
			e.TTFTMs = (1-gossipAlpha)*e.TTFTMs + gossipAlpha*peer.TTFTMs
			e.TTFTDevMs = (1-gossipAlpha)*e.TTFTDevMs + gossipAlpha*peer.TTFTDevMs
			e.TTFTN += peer.TTFTN
		}
	}

	if peer.TPSN > 0 {
		if e.TPSN == 0 {
			e.TPS = peer.TPS
			e.TPSN = peer.TPSN
		} else {
			e.TPS = (1-gossipAlpha)*e.TPS + gossipAlpha*peer.TPS
			e.TPSN += peer.TPSN
		}
	}

	if peer.ErrN > 0 {
		if e.ErrN == 0 {
			e.ErrRate = peer.ErrRate
			e.ErrN = peer.ErrN
		} else {
			e.ErrRate = (1-gossipAlpha)*e.ErrRate + gossipAlpha*peer.ErrRate
			e.ErrN += peer.ErrN
		}
	}

	if peer.Degraded && !e.Degraded {
		e.breaches++
		if e.breaches >= t.cfg.Breaches {
			now := t.now()
			e.Degraded, e.Since, e.Reason = true, now, "peer_degraded"
			e.lastBreach = now
		}
	}

	t.evaluate(tg, e)
}

func (t *Tracker) evaluate(tg router.Target, e *entry) {
	slo, ok := t.slos[tg.String()]
	if !ok {
		return
	}
	min := t.cfg.MinSamples
	var reasons []string
	if slo.TTFTMs > 0 && e.TTFTN >= min && e.TTFTMs > slo.TTFTMs {
		reasons = append(reasons, "ttft")
	}
	if slo.MinTPS > 0 && e.TPSN >= min/2+1 && e.TPS < slo.MinTPS {
		reasons = append(reasons, "tokens_per_sec")
	}
	if slo.MaxErrorRate > 0 && e.ErrN >= min && e.ErrRate > slo.MaxErrorRate {
		reasons = append(reasons, "error_rate")
	}
	now := t.now()
	if len(reasons) > 0 {
		e.breaches++
		e.lastBreach = now
		if !e.Degraded && e.breaches >= t.cfg.Breaches {
			e.Degraded, e.Since, e.Reason = true, now, strings.Join(reasons, ",")
		}
		return
	}
	// Only reset breaches if we had sufficient samples to positively confirm healthy state
	if (slo.TTFTMs > 0 && e.TTFTN >= min) || (slo.MaxErrorRate > 0 && e.ErrN >= min) || (slo.MinTPS > 0 && e.TPSN >= min/2+1) {
		e.breaches = 0
	}
	if e.Degraded && now.Sub(e.lastBreach) >= t.cfg.Recover {
		e.Degraded, e.Since, e.Reason = false, now, ""
	}
}

func (t *Tracker) Degraded(tg router.Target) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[tg]
	return ok && e.Degraded
}

func (t *Tracker) Stats(tg router.Target) Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.get(tg).Stats
}

func (t *Tracker) Snapshot() map[string]Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]Stats, len(t.m))
	for k, e := range t.m {
		out[k.String()] = e.Stats
	}
	return out
}

func (t *Tracker) DegradedTargets() []router.Target {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []router.Target
	for k, e := range t.m {
		if e.Degraded {
			out = append(out, k)
		}
	}
	return out
}

func (t *Tracker) HedgeDelay(tg router.Target, def time.Duration) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.m[tg]
	if !ok || e.TTFTN < t.cfg.MinSamples {
		return def
	}
	d := time.Duration((e.TTFTMs + 2*e.TTFTDevMs) * float64(time.Millisecond))
	return min(max(d, 50*time.Millisecond), 5*time.Second)
}
