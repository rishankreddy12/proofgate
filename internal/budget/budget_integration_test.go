//go:build integration

package budget

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func TestRedisLedger(t *testing.T) {
	ctx := context.Background()
	c, err := tcredis.Run(ctx, "redis/redis-stack-server:7.4.0-v1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	u, _ := c.ConnectionString(ctx)
	opt, _ := redis.ParseURL(u)
	l := NewRedisLedger(redis.NewClient(opt))

	v, err := l.Spent(ctx, "t", "2026-09")
	require.NoError(t, err)
	require.EqualValues(t, 0, v)
	require.NoError(t, l.Add(ctx, "t", "2026-09", 250))
	require.NoError(t, l.Add(ctx, "t", "2026-09", 750))
	v, _ = l.Spent(ctx, "t", "2026-09")
	require.EqualValues(t, 1000, v)
}
