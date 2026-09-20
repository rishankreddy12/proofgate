//go:build integration

package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/store"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func redisURL(t *testing.T) string {
	ctx := context.Background()
	c, err := tcredis.Run(ctx, "redis/redis-stack-server:7.4.0-v1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	u, err := c.ConnectionString(ctx)
	require.NoError(t, err)
	return u
}

func client(t *testing.T, url string) *redis.Client {
	opt, err := redis.ParseURL(url)
	require.NoError(t, err)
	return redis.NewClient(opt)
}

func TestThreeReplicasShareOneLimit(t *testing.T) {
	url := redisURL(t)
	replicas := []*Redis{NewRedis(client(t, url)), NewRedis(client(t, url)), NewRedis(client(t, url))}
	p := store.TenantPolicy{RPM: 10}
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, err := replicas[i%3].Take(context.Background(), "tenant-1", p, 0)
			require.NoError(t, err)
			if d.Allowed {
				allowed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.EqualValues(t, 10, allowed.Load())
}

func TestTokenBucket(t *testing.T) {
	l := NewRedis(client(t, redisURL(t)))
	ctx := context.Background()
	p := store.TenantPolicy{TPM: 1000}

	d, err := l.Take(ctx, "t2", p, 600)
	require.NoError(t, err)
	require.True(t, d.Allowed)

	d, _ = l.Take(ctx, "t2", p, 600)
	require.False(t, d.Allowed)
	// 200 tokens missing at 1000/60s = 12s
	require.InDelta(t, 12*time.Second, d.RetryAfter, float64(500*time.Millisecond))

	d, _ = l.Take(ctx, "t2", p, 1500)
	require.True(t, d.TooLarge)

	require.NoError(t, l.Adjust(ctx, "t2", p, 600)) // refund
	d, _ = l.Take(ctx, "t2", p, 600)
	require.True(t, d.Allowed)
}
