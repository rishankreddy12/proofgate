package router

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/provider"
	"github.com/stretchr/testify/require"
)



func TestHedgeWinsWhenPrimaryIsSlow(t *testing.T) {
	var cancelled atomic.Bool
	fn := func(ctx context.Context, t Target) (string, error) {
		if t == A {
			select {
			case <-time.After(time.Second):
				return "slow", nil
			case <-ctx.Done():
				cancelled.Store(true)
				return "", ctx.Err()
			}
		}
		time.Sleep(20 * time.Millisecond)
		return "fast", nil
	}
	start := time.Now()
	v, res, hedged, err := ExecuteHedged(context.Background(), []Target{A, B}, RetryPolicy{MaxAttempts: 1}, nil,
		50*time.Millisecond, func() bool { return true }, fn, nil)
	require.NoError(t, err)
	require.True(t, hedged)
	require.Equal(t, "fast", v)
	require.Equal(t, B, res.Target)
	require.Less(t, time.Since(start), 300*time.Millisecond)
	require.Eventually(t, cancelled.Load, time.Second, 5*time.Millisecond, "the loser is cancelled")
}

func TestNoHedgeWhenPrimaryIsFastOrBudgetExhausted(t *testing.T) {
	calls := atomic.Int32{}
	fn := func(_ context.Context, t Target) (string, error) {
		calls.Add(1)
		if t == A {
			time.Sleep(100 * time.Millisecond)
		}
		return t.Provider, nil
	}
	v, _, hedged, err := ExecuteHedged(context.Background(), []Target{A, B}, RetryPolicy{MaxAttempts: 1}, nil,
		50*time.Millisecond, func() bool { return false }, fn, nil)
	require.NoError(t, err)
	require.False(t, hedged)
	require.Equal(t, "a", v)
	require.EqualValues(t, 1, calls.Load())
}

func TestBothFailFallsThroughToRest(t *testing.T) {
	C := Target{"c", "m"}
	noSleep(t)
	fn := func(_ context.Context, t Target) (string, error) {
		if t == C {
			return "c", nil
		}
		time.Sleep(10 * time.Millisecond)
		return "", &provider.Error{Status: 503, Retryable: true}
	}
	v, res, _, err := ExecuteHedged(context.Background(), []Target{A, B, C}, RetryPolicy{MaxAttempts: 1}, nil,
		time.Millisecond, func() bool { return true }, fn, nil)
	require.NoError(t, err)
	require.Equal(t, "c", v)
	require.Equal(t, C, res.Target)
}

func TestLoserSuccessIsDiscarded(t *testing.T) {
	var discarded atomic.Int32
	fn := func(ctx context.Context, t Target) (string, error) {
		if t == A {
			time.Sleep(80 * time.Millisecond) // ignores cancellation and succeeds late
			return "late", nil
		}
		time.Sleep(20 * time.Millisecond)
		return "fast", nil
	}
	_, _, _, err := ExecuteHedged(context.Background(), []Target{A, B}, RetryPolicy{MaxAttempts: 1}, nil,
		10*time.Millisecond, func() bool { return true }, fn, func(string) { discarded.Add(1) })
	require.NoError(t, err)
	require.Eventually(t, func() bool { return discarded.Load() == 1 }, time.Second, 5*time.Millisecond)
}

func TestHedgeBudget(t *testing.T) {
	b := NewHedgeBudget(0.1)
	for i := 0; i < 100; i++ {
		b.Request()
	}
	n := 0
	for b.Allow() {
		n++
	}
	require.Equal(t, 10, n)
}
