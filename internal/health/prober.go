// Package health provides active monitoring, circuit breaking, and cross-node gossip.
package health

import (
	"context"
	"time"

	"github.com/proofgate/proofgate/internal/router"
)

// Prober executes continuous synthetic health checks ("pings") against degraded provider targets.
//
// Once a target is marked as 'Degraded' by the Tracker (e.g. due to consecutive 5xx errors),
// it is removed from live routing traffic. The Prober takes over, sending tiny synthetic
// payloads (like "ping") in the background. If the synthetic probe succeeds, the target is
// reinstated into the live traffic pool.
type Prober struct {
	tr    *Tracker
	send  func(ctx context.Context, t router.Target) (time.Duration, error)
	every time.Duration
}

// NewProber initializes a background synthetic Prober mechanism.
func NewProber(tr *Tracker, send func(ctx context.Context, t router.Target) (time.Duration, error), every time.Duration) *Prober {
	return &Prober{tr: tr, send: send, every: every}
}

// Tick executes a single synchronous sweep: sending synthetic probes to all currently degraded targets.
// The result of each probe (OK or Failed) is immediately fed back into the central Tracker.
func (p *Prober) Tick(ctx context.Context) {
	for _, t := range p.tr.DegradedTargets() {
		ttft, err := p.send(ctx, t)
		if err != nil {
			p.tr.Observe(Sample{Target: t, Outcome: Failed})
			continue
		}
		p.tr.Observe(Sample{Target: t, TTFT: ttft, Outcome: OK})
	}
}

// Run launches an infinite ticker loop, executing synthetic probes at the configured interval.
// It stops gracefully when the parent Context is cancelled.
func (p *Prober) Run(ctx context.Context) {
	if p.every <= 0 {
		return
	}
	t := time.NewTicker(p.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Tick(ctx)
		}
	}
}
