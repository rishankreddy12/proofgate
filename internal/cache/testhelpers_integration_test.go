//go:build integration

package cache

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// redisClient starts Redis Stack (search module included) and returns a RESP2 client.
func redisClient(t *testing.T) *redis.Client {
	ctx := context.Background()
	c, err := tcredis.Run(ctx, "redis/redis-stack-server:7.4.0-v1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	u, err := c.ConnectionString(ctx)
	require.NoError(t, err)
	opt, err := redis.ParseURL(u)
	require.NoError(t, err)
	opt.Protocol = 2
	return redis.NewClient(opt)
}
