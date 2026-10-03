// Package router provides enterprise-grade capabilities, configuration, and structural components for the router subsystem.
package router

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/proofgate/proofgate/internal/api"
)

// HedgeBudget is a concurrency-safe token bucket mechanism ensuring that
// speculative "hedge" requests do not overwhelm upstream providers.
// It limits hedges to a maximum ratio (`maxExtra`) relative to total traffic volume.
type HedgeBudget struct {
	mu       sync.Mutex
	maxExtra float64
	requests float64
	hedges   float64
}

// NewHedgeBudget initializes a new budget manager enforcing the specified hedge-to-request ratio.
func NewHedgeBudget(maxExtra float64) *HedgeBudget { return &HedgeBudget{maxExtra: maxExtra} }

// decay shifts the sliding window to prevent indefinite accumulation.
func (b *HedgeBudget) decay() {
	if b.requests > 1000 { // keep a sliding window without storing timestamps
		f := 1000 / b.requests
		b.requests, b.hedges = 1000, b.hedges*f
	}
}

// Request registers a standard inbound request with the budget window.
func (b *HedgeBudget) Request() {
	b.mu.Lock()
	b.requests++
	b.decay()
	b.mu.Unlock()
}

// Allow evaluates if the current traffic volume permits an additional hedged request.
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

// ExecuteHedged runs an execution plan using Tail-Latency Hedging.
//
// Protocol:
// 1. It dispatches a request to the primary target in the plan.
// 2. It waits for the `delay` duration (e.g. 95th percentile TTFT).
// 3. If the primary has not returned, it dispatches a secondary parallel request to the next target in the plan.
// 4. It races the two requests. The first to return successfully is returned to the client, and the loser is eagerly cancelled.
func ExecuteHedged[T any](ctx context.Context, plan []Target, rp RetryPolicy, br *Breakers, deadline time.Duration, delay time.Duration,
	allow func() bool, fn func(ctx context.Context, t Target) (T, error), discard func(T)) (T, Result, bool, error) {
	var zero T
	if deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deadline)
		defer cancel()
	}

	run := func(plan []Target) (T, Result, error) {
		var v T
		res, err := Execute(ctx, plan, rp, br, 0, func(ctx context.Context, t Target) error {
			x, err := fn(ctx, t)
			if err == nil {
				v = x
			}
			return err
		})
		return v, res, err
	}

	// If only 1 target exists, fallback to standard linear execution
	if len(plan) < 2 {
		v, res, err := run(plan)
		return v, res, false, err
	}

	var won bool
	ctxA, cancelA := context.WithCancel(ctx)
	defer func() {
		if !won {
			cancelA()
		}
	}()
	ctxB, cancelB := context.WithCancel(ctx)
	defer func() {
		if !won {
			cancelB()
		}
	}()

	results := make(chan outcome[T], 2)
	launch := func(c context.Context, t Target) {
		go func() {
			v, err := fn(c, t)
			results <- outcome[T]{v: v, err: err, t: t}
		}()
	}

	// Step 1: Launch primary
	launch(ctxA, plan[0])
	timer := time.NewTimer(delay)
	defer timer.Stop()

	hedged, pending := false, 1
	var lastErr error

	finish := func(winner outcome[T], cancelOther context.CancelFunc) (T, Result, bool, error) {
		won = true
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
			// Step 2 & 3: Delay elapsed, launch secondary if budget allows
			if !hedged && allow() {
				hedged = true
				pending++
				launch(ctxB, plan[1])
			}
		case o := <-results:
			// Step 4: A request finished, process outcome
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
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				msg := "gateway timeout: plan deadline exceeded"
				if lastErr != nil {
					msg = fmt.Sprintf("gateway timeout: plan deadline exceeded (last attempt: %v)", lastErr)
				}
				return zero, Result{}, hedged, api.GatewayTimeout(msg)
			}
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
