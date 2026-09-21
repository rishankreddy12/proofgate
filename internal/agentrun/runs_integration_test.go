//go:build integration

package agentrun

import (
	"context"
	"testing"

	"github.com/proofgate/proofgate/internal/store"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func rs(t *testing.T) *RedisStore {
	ctx := context.Background()
	for _, addr := range []string{"127.0.0.1:6379", "127.0.0.1:16379"} {
		rdb := redis.NewClient(&redis.Options{Addr: addr})
		if err := rdb.Ping(ctx).Err(); err == nil {
			return NewRedisStore(rdb)
		}
	}
	c, err := tcredis.Run(ctx, "redis/redis-stack-server:7.4.0-v1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	u, _ := c.ConnectionString(ctx)
	opt, _ := redis.ParseURL(u)
	return NewRedisStore(redis.NewClient(opt))
}

func TestStepsCostAndLoops(t *testing.T) {
	s := rs(t)
	ctx := context.Background()
	p := store.RunPolicy{MaxSteps: 4, MaxCostUSD: 0.001}.WithDefaults()

	r, err := s.Step(ctx, "t", "run-1", p, "fp-a")
	require.NoError(t, err)
	require.Equal(t, Allowed, r.Status)
	r, _ = s.Step(ctx, "t", "run-1", p, "fp-a")
	require.Equal(t, Allowed, r.Status)
	r, _ = s.Step(ctx, "t", "run-1", p, "fp-a")
	require.Equal(t, Loop, r.Status, "third identical step in the window")
	require.Equal(t, 3, r.Repeats)

	r, _ = s.Step(ctx, "t", "run-1", p, "fp-b")
	require.Equal(t, Allowed, r.Status)
	require.Equal(t, 3, r.Steps)

	require.NoError(t, s.Charge(ctx, "t", "run-1", p, 1000, 50)) // $0.001 spent
	r, _ = s.Step(ctx, "t", "run-1", p, "fp-c")
	require.Equal(t, CostExceeded, r.Status)

	q := store.RunPolicy{MaxSteps: 2}.WithDefaults()
	_, _ = s.Step(ctx, "t", "run-2", q, "x")
	_, _ = s.Step(ctx, "t", "run-2", q, "y")
	r, _ = s.Step(ctx, "t", "run-2", q, "z")
	require.Equal(t, StepsExceeded, r.Status)
}
