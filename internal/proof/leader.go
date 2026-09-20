package proof

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type LeaderElection interface {
	IsLeader() bool
}

type Leader struct {
	rdb        redis.UniversalClient
	instanceID string
	key        string
	ttl        time.Duration
	renewEvery time.Duration

	mu       sync.RWMutex
	isLeader bool
	stop     chan struct{}
	done     chan struct{}
}

func NewLeader(rdb redis.UniversalClient, instanceID, key string, ttl, renewEvery time.Duration) *Leader {
	if key == "" {
		key = "proof:leader"
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	if renewEvery <= 0 {
		renewEvery = 10 * time.Second
	}
	return &Leader{
		rdb:        rdb,
		instanceID: instanceID,
		key:        key,
		ttl:        ttl,
		renewEvery: renewEvery,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

func (l *Leader) IsLeader() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.isLeader
}

func (l *Leader) setLeader(val bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.isLeader = val
}

var renewScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("pexpire", KEYS[1], ARGV[2])
else
    return 0
end
`)

var releaseScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
else
    return 0
end
`)

func (l *Leader) Step(ctx context.Context) {
	if l.IsLeader() {
		// Attempt renewal
		ttlMs := l.ttl.Milliseconds()
		res, err := renewScript.Run(ctx, l.rdb, []string{l.key}, l.instanceID, ttlMs).Int64()
		if err != nil || res == 0 {
			l.setLeader(false)
		}
	} else {
		// Attempt acquisition
		ok, err := l.rdb.SetNX(ctx, l.key, l.instanceID, l.ttl).Result()
		if err == nil && ok {
			l.setLeader(true)
		}
	}
}

func (l *Leader) Start(ctx context.Context) {
	go func() {
		defer close(l.done)
		// Run initial attempt immediately
		l.Step(ctx)

		ticker := time.NewTicker(l.renewEvery)
		defer ticker.Stop()

		for {
			select {
			case <-l.stop:
				l.release(context.Background())
				return
			case <-ctx.Done():
				l.release(context.Background())
				return
			case <-ticker.C:
				l.Step(ctx)
			}
		}
	}()
}

func (l *Leader) release(ctx context.Context) {
	if l.IsLeader() {
		_, _ = releaseScript.Run(ctx, l.rdb, []string{l.key}, l.instanceID).Result()
		l.setLeader(false)
	}
}

func (l *Leader) Stop() {
	close(l.stop)
	<-l.done
}
