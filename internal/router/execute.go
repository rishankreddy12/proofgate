package router

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/provider"
)

type Attempt func(ctx context.Context, t Target) error

type Result struct {
	Target   Target
	Attempts int
}

var sleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// backoff is "full jitter": uniform in [0, min(2s, base*2^n)].
func backoff(base time.Duration, n int) time.Duration {
	ceiling := min(base<<n, 2*time.Second)
	if ceiling <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(ceiling) + 1))
}

type decision int

const (
	done decision = iota
	retrySame
	nextTarget
)

func classify(err error) (decision, bool /*breaker failure*/) {
	var pe *provider.Error
	if !errors.As(err, &pe) {
		var ae *api.Error
		if errors.As(err, &ae) {
			return done, false // our own validation error: never fail over
		}
		return retrySame, true
	}
	switch {
	case pe.Status == 429:
		return nextTarget, false
	case pe.Status == 401 || pe.Status == 403:
		return nextTarget, true
	case pe.Retryable:
		return retrySame, true
	default:
		return done, false
	}
}

// Execute runs fn over the plan following the failover rules in the plan document.
func Execute(ctx context.Context, plan []Target, rp RetryPolicy, br *Breakers, fn Attempt) (Result, error) {
	var res Result
	var lastErr error = api.NoHealthyTarget()
	maxAttempts := max(rp.MaxAttempts, 1)
	for ti, t := range plan {
		isLast := ti == len(plan)-1
		if br != nil && !br.Allow(t) && !isLast {
			continue
		}
		for n := 0; n < maxAttempts; n++ {
			if err := ctx.Err(); err != nil {
				return res, err
			}
			res.Attempts++
			res.Target = t
			err := fn(ctx, t)
			if err == nil {
				if br != nil {
					br.Success(t)
				}
				return res, nil
			}
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			lastErr = err
			d, breakerFail := classify(err)
			if breakerFail && br != nil {
				br.Failure(t)
			}
			if d == done {
				return res, err
			}
			if d == nextTarget {
				break
			}
			if n < maxAttempts-1 {
				if err := sleep(ctx, backoff(rp.BaseDelay, n)); err != nil {
					return res, err
				}
			}
		}
	}
	return res, lastErr
}
