package health

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func testRedis(t *testing.T) redis.UniversalClient {
	ctx := context.Background()
	for _, addr := range []string{"127.0.0.1:6379", "127.0.0.1:16379"} {
		rdb := redis.NewClient(&redis.Options{Addr: addr})
		if err := rdb.Ping(ctx).Err(); err == nil {
			return rdb
		}
	}
	c, err := tcredis.Run(ctx, "redis:7.4-alpine")
	if err != nil {
		t.Skip("docker/redis not available for redis health integration test")
		return nil
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	u, _ := c.ConnectionString(ctx)
	opt, _ := redis.ParseURL(u)
	return redis.NewClient(opt)
}

func TestRedisHealth_CrossReplicaSync(t *testing.T) {
	rdb := testRedis(t)
	if rdb == nil {
		return
	}
	defer rdb.Close()
	ctx := context.Background()

	tg := router.Target{Provider: "openai", Model: "gpt-4o"}
	now := time.Now()

	tr1 := NewTracker(config.HealthConfig{Alpha: 0.5, Breaches: 2, Recover: 30 * time.Second, MinSamples: 2},
		map[string]config.SLO{"openai/gpt-4o": {TTFTMs: 300, MinTPS: 20, MaxErrorRate: 0.5}}, func() time.Time { return now })
	tr2 := NewTracker(config.HealthConfig{Alpha: 0.5, Breaches: 2, Recover: 30 * time.Second, MinSamples: 2},
		map[string]config.SLO{"openai/gpt-4o": {TTFTMs: 300, MinTPS: 20, MaxErrorRate: 0.5}}, func() time.Time { return now })

	tr1.SetTargets([]router.Target{tg})
	tr2.SetTargets([]router.Target{tg})

	rh1 := NewRedisHealth(rdb, "replica-1", tr1, []router.Target{tg}, 2*time.Second)
	rh2 := NewRedisHealth(rdb, "replica-2", tr2, []router.Target{tg}, 2*time.Second)

	// Clean up key beforehand
	key := "health:" + tg.String()
	_ = rdb.Del(ctx, key).Err()
	t.Cleanup(func() { _ = rdb.Del(ctx, key).Err() })

	// tr1 observes some high latency samples (degrading the target)
	for i := 0; i < 4; i++ {
		tr1.Observe(Sample{Target: tg, TTFT: 1000 * time.Millisecond, Outcome: OK})
	}
	require.True(t, tr1.Degraded(tg), "tr1 should be degraded")
	require.False(t, tr2.Degraded(tg), "tr2 has not observed anything yet")

	// rh1 publishes snapshot to Redis
	require.NoError(t, rh1.SyncOnce(ctx))

	// Verify key exists and contains replica-1 hash field
	val, err := rdb.HGet(ctx, key, "replica-1").Result()
	require.NoError(t, err)
	require.Contains(t, val, "updated_at")

	// rh2 pulls snapshot from Redis
	require.NoError(t, rh2.SyncOnce(ctx))

	// Verify tr2 received degraded status and merged stats
	require.True(t, tr2.Degraded(tg), "tr2 should now recognize degradation via Redis health sharing")
	s2 := tr2.Stats(tg)
	require.InDelta(t, 1000.0, s2.TTFTMs, 1e-1)
}

func TestRedisHealth_StaleEntryCleanup(t *testing.T) {
	rdb := testRedis(t)
	if rdb == nil {
		return
	}
	defer rdb.Close()
	ctx := context.Background()

	tg := router.Target{Provider: "anthropic", Model: "claude-3-5-sonnet"}
	now := time.Now()

	tr := NewTracker(config.HealthConfig{Alpha: 0.5, Breaches: 2, Recover: 30 * time.Second, MinSamples: 2}, nil, func() time.Time { return now })
	tr.SetTargets([]router.Target{tg})

	rh := NewRedisHealth(rdb, "replica-active", tr, []router.Target{tg}, 2*time.Second)

	key := "health:" + tg.String()
	_ = rdb.Del(ctx, key).Err()
	t.Cleanup(func() { _ = rdb.Del(ctx, key).Err() })

	// Insert stale peer entry (published 60 seconds ago; TTL is 6 seconds)
	stalePayload := peerHealthPayload{
		UpdatedAt: time.Now().Add(-60 * time.Second),
		Stats: Stats{
			TTFTMs:   999,
			TTFTN:    10,
			Degraded: true,
		},
	}
	raw, _ := json.Marshal(stalePayload)
	require.NoError(t, rdb.HSet(ctx, key, "replica-dead", string(raw)).Err())

	// PullOnce should detect it as stale, clean it up from Redis (HDel), and NOT merge it into tracker
	require.NoError(t, rh.SyncOnce(ctx))

	// Verify tracker did not merge dead replica's stats
	require.False(t, tr.Degraded(tg))
	require.Equal(t, 0, tr.Stats(tg).TTFTN)

	// Verify stale replica entry was deleted from the hash
	exists, err := rdb.HExists(ctx, key, "replica-dead").Result()
	require.NoError(t, err)
	require.False(t, exists, "stale replica entry should be purged from Redis hash")
}
