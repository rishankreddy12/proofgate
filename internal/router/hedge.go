package router

import (
	"context"
	"sync"
	"time"
)

// HedgeBudget caps hedges at maxExtra × requests over the last 1,000 requests.
type HedgeBudget struct {
	mu       sync.Mutex
	maxExtra float64
	requests float64
	hedges   float64
}

func NewHedgeBudget(maxExtra float64) *HedgeBudget { return &HedgeBudget{maxExtra: maxExtra} }

func (b *HedgeBudget) decay() {
	if b.requests > 1000 { // keep a sliding window without storing timestamps
		f := 1000 / b.requests
		b.requests, b.hedges = 1000, b.hedges*f
	}
}

func (b *HedgeBudget) Request() {
	b.mu.Lock()
	b.requests++
	b.decay()
	b.mu.Unlock()
}

func (b *HedgeBudget) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hedges+1 > b.maxExtra*b.requests {
		return false
	}
	b.hedges++
	return true
}

type outcome[T any] struct {
	v   T
	err error
	t   Target
}

func ExecuteHedged[T any](ctx context.Context, plan []Target, rp RetryPolicy, br *Breakers, delay time.Duration,
	allow func() bool, fn func(ctx context.Context, t Target) (T, error), discard func(T)) (T, Result, bool, error) {
	var zero T
	run := func(plan []Target) (T, Result, error) {
		var v T
		res, err := Execute(ctx, plan, rp, br, func(ctx context.Context, t Target) error {
			x, err := fn(ctx, t)
			if err == nil {
				v = x
			}
			return err
		})
		return v, res, err
	}
	if len(plan) < 2 {
		v, res, err := run(plan)
		return v, res, false, err
	}
	ctxA, cancelA := context.WithCancel(ctx)
	ctxB, cancelB := context.WithCancel(ctx)
	results := make(chan outcome[T], 2)
	launch := func(c context.Context, t Target) {
		go func() {
			v, err := fn(c, t)
			results <- outcome[T]{v: v, err: err, t: t}
		}()
	}
	launch(ctxA, plan[0])
	timer := time.NewTimer(delay)
	defer timer.Stop()
	hedged, pending := false, 1
	var lastErr error
	finish := func(winner outcome[T], cancelOther context.CancelFunc) (T, Result, bool, error) {
		cancelOther()
		if pending > 1 && discard != nil { // the other attempt may still succeed; release it
			go func(n int) {
				for i := 0; i < n; i++ {
					if o := <-results; o.err == nil {
						discard(o.v)
					}
				}
			}(pending - 1)
		}
		if br != nil {
			br.Success(winner.t)
		}
		attempts := 1
		if hedged {
			attempts = 2
		}
		return winner.v, Result{Target: winner.t, Attempts: attempts}, hedged, nil
	}
	for {
		select {
		case <-timer.C:
			if !hedged && allow() {
				hedged = true
				pending++
				launch(ctxB, plan[1])
			}
		case o := <-results:
			if o.err == nil {
				if o.t == plan[0] {
					return finish(o, cancelB)
				}
				return finish(o, cancelA)
			}
			lastErr = o.err
			if br != nil {
				if _, fail := classify(o.err); fail {
					br.Failure(o.t)
				}
			}
			pending--
			if pending == 0 {
				rest := plan[1:]
				if hedged {
					rest = plan[2:]
				}
				cancelA()
				cancelB()
				if len(rest) == 0 {
					return zero, Result{Target: o.t, Attempts: 2}, hedged, lastErr
				}
				if d, _ := classify(o.err); d == done {
					return zero, Result{Target: o.t}, hedged, o.err
				}
				v, res, err := run(rest)
				res.Attempts += 1 + boolInt(hedged)
				return v, res, hedged, err
			}
		case <-ctx.Done():
			cancelA()
			cancelB()
			return zero, Result{}, hedged, ctx.Err()
		}
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
