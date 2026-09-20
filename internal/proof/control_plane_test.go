package proof

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func redisClient(t *testing.T) redis.UniversalClient {
	ctx := context.Background()
	// First attempt to connect to local compose redis
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:16379"})
	if err := rdb.Ping(ctx).Err(); err == nil {
		return rdb
	}

	// Fallback to testcontainers
	c, err := tcredis.Run(ctx, "redis/redis-stack-server:7.4.0-v1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	u, err := c.ConnectionString(ctx)
	require.NoError(t, err)
	opt, err := redis.ParseURL(u)
	require.NoError(t, err)
	return redis.NewClient(opt)
}

func TestLeader_AcquireRenewLost(t *testing.T) {
	rdb := redisClient(t)
	ctx := context.Background()
	key := "test:proof:leader:" + time.Now().Format(time.RFC3339Nano)

	l1 := NewLeader(rdb, "inst-1", key, 2*time.Second, 100*time.Millisecond)
	l2 := NewLeader(rdb, "inst-2", key, 2*time.Second, 100*time.Millisecond)

	// Step 1: l1 acquires
	l1.Step(ctx)
	require.True(t, l1.IsLeader())

	// Step 2: l2 cannot acquire
	l2.Step(ctx)
	require.False(t, l2.IsLeader())

	// Step 3: l1 renews successfully
	l1.Step(ctx)
	require.True(t, l1.IsLeader())

	// Step 4: External takeover (e.g. timeout or stolen)
	rdb.Set(ctx, key, "inst-hijacker", 2*time.Second)

	// l1 renewal fails, forfeits leadership
	l1.Step(ctx)
	require.False(t, l1.IsLeader())

	// Cleanup
	rdb.Del(ctx, key)
}

func TestShadowSpendGuard_LimitEnforced(t *testing.T) {
	rdb := redisClient(t)
	ctx := context.Background()

	guard := NewShadowSpendGuard(rdb, 5000)
	// Use unique date for test isolation
	fixedDate := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	guard.SetNow(func() time.Time { return fixedDate })
	rdb.Del(ctx, guard.todayKey())

	// Initially 0 spent
	spent, err := guard.SpentToday(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, spent)

	can, err := guard.CanSpend(ctx, 3000)
	require.NoError(t, err)
	require.True(t, can)

	require.NoError(t, guard.AddSpend(ctx, 3000))

	spent, _ = guard.SpentToday(ctx)
	require.EqualValues(t, 3000, spent)

	// Can spend 1000 more (3000 + 1000 <= 5000)
	can, err = guard.CanSpend(ctx, 1000)
	require.NoError(t, err)
	require.True(t, can)

	// Cannot spend 3000 more (3000 + 3000 > 5000)
	can, err = guard.CanSpend(ctx, 3000)
	require.NoError(t, err)
	require.False(t, can)

	// Add 2000, reaching the limit exactly
	require.NoError(t, guard.AddSpend(ctx, 2000))

	// Now even 1 micro cannot be spent
	can, err = guard.CanSpend(ctx, 1)
	require.NoError(t, err)
	require.False(t, can)

	rdb.Del(ctx, guard.todayKey())
}
