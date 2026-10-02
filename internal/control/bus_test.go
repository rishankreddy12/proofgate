package control

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func TestBus_DispatchDirect(t *testing.T) {
	ctx := context.Background()
	bus := NewBus(nil, "replica-1", "test:ctl")

	var purgeSecretsCalled atomic.Bool
	var purgeCacheCalled atomic.Bool
	var wildcardCalled atomic.Int32

	bus.Subscribe(OpPurgeSecrets, func(c context.Context, msg Message) error {
		purgeSecretsCalled.Store(true)
		require.Equal(t, "replica-2", msg.Sender)
		return nil
	})

	bus.Subscribe(OpPurgeCache, func(c context.Context, msg Message) error {
		purgeCacheCalled.Store(true)
		return nil
	})

	bus.Subscribe("*", func(c context.Context, msg Message) error {
		wildcardCalled.Add(1)
		return nil
	})

	// Dispatch purge_secrets
	bus.Dispatch(ctx, Message{
		Op:     OpPurgeSecrets,
		Sender: "replica-2",
		TS:     time.Now().UTC(),
	})

	require.True(t, purgeSecretsCalled.Load())
	require.False(t, purgeCacheCalled.Load())
	require.Equal(t, int32(1), wildcardCalled.Load())

	// Dispatch purge_cache
	bus.Dispatch(ctx, Message{
		Op:     OpPurgeCache,
		Sender: "replica-3",
		TS:     time.Now().UTC(),
	})

	require.True(t, purgeCacheCalled.Load())
	require.Equal(t, int32(2), wildcardCalled.Load())
}

func testRedis(t *testing.T) redis.UniversalClient {
	ctx := context.Background()
	for _, addr := range []string{"127.0.0.1:6379", "127.0.0.1:16379"} {
		rdb := redis.NewClient(&redis.Options{Addr: addr})
		if err := rdb.Ping(ctx).Err(); err == nil {
			return rdb
		}
	}
	c, err := tcredis.Run(ctx, "redis/redis-stack-server:7.4.0-v1")
	if err != nil {
		t.Skip("docker/redis not available for pubsub integration test")
		return nil
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	u, _ := c.ConnectionString(ctx)
	opt, _ := redis.ParseURL(u)
	return redis.NewClient(opt)
}

func TestBus_CrossReplicaPubSub(t *testing.T) {
	rdb := testRedis(t)
	if rdb == nil {
		return
	}
	defer rdb.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	channel := "test:ctl:" + time.Now().Format("150405.000000")

	// Two replicas sharing the same Redis
	replicaA := NewBus(rdb, "gateway-1", channel)
	replicaB := NewBus(rdb, "gateway-2", channel)

	var (
		mu           sync.Mutex
		receivedByB  []Message
		receivedByA  []Message
		receivedDone = make(chan struct{}, 2)
	)

	replicaB.Subscribe(OpPurgeSecrets, func(_ context.Context, msg Message) error {
		mu.Lock()
		receivedByB = append(receivedByB, msg)
		mu.Unlock()
		receivedDone <- struct{}{}
		return nil
	})

	replicaA.Subscribe(OpKeyRevoked, func(_ context.Context, msg Message) error {
		mu.Lock()
		receivedByA = append(receivedByA, msg)
		mu.Unlock()
		receivedDone <- struct{}{}
		return nil
	})

	go func() { _ = replicaA.Run(ctx) }()
	go func() { _ = replicaB.Run(ctx) }()

	// Wait for subscriptions to be active
	time.Sleep(100 * time.Millisecond)

	// Replica A publishes PurgeSecrets
	err := replicaA.Publish(ctx, OpPurgeSecrets, map[string]string{"reason": "admin_action"})
	require.NoError(t, err)

	select {
	case <-receivedDone:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Replica B to receive OpPurgeSecrets")
	}

	mu.Lock()
	require.Len(t, receivedByB, 1)
	require.Equal(t, OpPurgeSecrets, receivedByB[0].Op)
	require.Equal(t, "gateway-1", receivedByB[0].Sender)
	require.Equal(t, "admin_action", receivedByB[0].Args["reason"])
	mu.Unlock()

	// Replica B publishes KeyRevoked
	err = replicaB.Publish(ctx, OpKeyRevoked, map[string]string{"key_hash": "abc123hash"})
	require.NoError(t, err)

	select {
	case <-receivedDone:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Replica A to receive OpKeyRevoked")
	}

	mu.Lock()
	require.Len(t, receivedByA, 1)
	require.Equal(t, OpKeyRevoked, receivedByA[0].Op)
	require.Equal(t, "gateway-2", receivedByA[0].Sender)
	require.Equal(t, "abc123hash", receivedByA[0].Args["key_hash"])
	mu.Unlock()
}
