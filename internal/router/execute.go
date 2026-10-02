package router

import (
	"context"
	"errors"
	"fmt"
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
func Execute(ctx context.Context, plan []Target, rp RetryPolicy, br *Breakers, deadline time.Duration, fn Attempt) (Result, error) {
	var res Result
	var lastErr error = api.NoHealthyTarget()
	var shortestRetryAfter time.Duration

	if deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deadline)
		defer cancel()
	}

	checkCtx := func() error {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				msg := "gateway timeout: plan deadline exceeded"
				if lastErr != nil {
					msg = fmt.Sprintf("gateway timeout: plan deadline exceeded (last attempt: %v)", lastErr)
				}
				return api.GatewayTimeout(msg)
			}
			return err
		}
		return nil
	}

	maxAttempts := max(rp.MaxAttempts, 1)
	for ti, t := range plan {
		isLast := ti == len(plan)-1
		if br != nil && !br.Allow(t) && !isLast {
			continue
		}
		for n := 0; n < maxAttempts; n++ {
			if err := checkCtx(); err != nil {
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
			lastErr = err
			var pe *provider.Error
			if errors.As(err, &pe) {
				if pe.RetryAfter > 0 {
					if br != nil {
						br.OpenFor(t, pe.RetryAfter)
					}
					if shortestRetryAfter == 0 || pe.RetryAfter < shortestRetryAfter {
						shortestRetryAfter = pe.RetryAfter
					}
				}
			}
			if err := checkCtx(); err != nil {
				return res, err
			}
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
					return res, checkCtx()
				}
			}
		}
	}
	if shortestRetryAfter > 0 {
		var pe *provider.Error
		var ae *api.Error
		if errors.As(lastErr, &pe) {
			pe.RetryAfter = shortestRetryAfter
		} else if errors.As(lastErr, &ae) {
			ae.RetryAfter = shortestRetryAfter
		}
	}
	return res, lastErr
}
