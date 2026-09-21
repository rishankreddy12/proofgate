package health

import (
	"context"
	"time"

	"github.com/proofgate/proofgate/internal/router"
)

// Prober sends a tiny request to each degraded target so it can recover without live traffic.
type Prober struct {
	tr    *Tracker
	send  func(ctx context.Context, t router.Target) (time.Duration, error)
	every time.Duration
}

func NewProber(tr *Tracker, send func(ctx context.Context, t router.Target) (time.Duration, error), every time.Duration) *Prober {
	return &Prober{tr: tr, send: send, every: every}
}

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
