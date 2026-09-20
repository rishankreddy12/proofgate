package router

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/provider"
	"github.com/stretchr/testify/require"
)

var A, B = Target{"a", "m"}, Target{"b", "m"}

func noSleep(t *testing.T) *[]time.Duration {
	var slept []time.Duration
	old := sleep
	sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	t.Cleanup(func() { sleep = old })
	return &slept
}

func run(t *testing.T, results map[Target][]error) (Result, error, []Target) {
	var calls []Target
	br := NewBreakers(5, time.Minute, time.Now)
	res, err := Execute(context.Background(), []Target{A, B}, RetryPolicy{MaxAttempts: 2, BaseDelay: 100 * time.Millisecond}, br,
		func(_ context.Context, tg Target) error {
			calls = append(calls, tg)
			q := results[tg]
			if len(q) == 0 {
				return nil
			}
			e := q[0]
			results[tg] = q[1:]
			return e
		})
	return res, err, calls
}

func perr(status int) error {
	return &provider.Error{Provider: "x", Status: status, Retryable: provider.RetryableStatus(status) || status == 0}
}

func TestRetryThenSucceedSameTarget(t *testing.T) {
	slept := noSleep(t)
	res, err, calls := run(t, map[Target][]error{A: {perr(503)}})
	require.NoError(t, err)
	require.Equal(t, A, res.Target)
	require.Equal(t, 2, res.Attempts)
	require.Equal(t, []Target{A, A}, calls)
	require.Len(t, *slept, 1)
	require.LessOrEqual(t, (*slept)[0], 100*time.Millisecond)
}

func TestFailoverAfterRetriesExhausted(t *testing.T) {
	noSleep(t)
	res, err, calls := run(t, map[Target][]error{A: {perr(500), perr(0)}})
	require.NoError(t, err)
	require.Equal(t, B, res.Target)
	require.Equal(t, []Target{A, A, B}, calls)
	require.Equal(t, 3, res.Attempts)
}

func TestRateLimitedSkipsToNextWithoutRetry(t *testing.T) {
	noSleep(t)
	_, err, calls := run(t, map[Target][]error{A: {perr(429)}})
	require.NoError(t, err)
	require.Equal(t, []Target{A, B}, calls)
}

func TestAuthErrorFailsOver(t *testing.T) {
	noSleep(t)
	_, err, calls := run(t, map[Target][]error{A: {perr(401)}})
	require.NoError(t, err)
	require.Equal(t, []Target{A, B}, calls)
}

func TestBadRequestDoesNotFailOver(t *testing.T) {
	noSleep(t)
	_, err, calls := run(t, map[Target][]error{A: {perr(400)}})
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	require.Equal(t, 400, pe.Status)
	require.Equal(t, []Target{A}, calls)
}

func TestAllFailReturnsLastError(t *testing.T) {
	noSleep(t)
	_, err, _ := run(t, map[Target][]error{A: {perr(500), perr(500)}, B: {perr(502), perr(503)}})
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	require.Equal(t, 503, pe.Status)
}

func TestCancelledContextStops(t *testing.T) {
	noSleep(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Execute(ctx, []Target{A, B}, RetryPolicy{MaxAttempts: 2}, NewBreakers(5, time.Minute, time.Now),
		func(ctx context.Context, _ Target) error { return ctx.Err() })
	require.True(t, errors.Is(err, context.Canceled))
}
