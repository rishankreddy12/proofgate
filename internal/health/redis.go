// Package health provides enterprise-grade capabilities, configuration, and structural components for the health subsystem.
package health

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/redis/go-redis/v9"
)

// peerHealthPayload encapsulates the serialized payload broadcast to Redis for cross-replica sharing.
type peerHealthPayload struct {
	UpdatedAt time.Time `json:"updated_at"`
	Stats     Stats     `json:"stats"`
}

// RedisHealth coordinates cross-replica health metrics exchange via Redis hashes.
// It serves as an alternative to the UDP-based Gossip mechanism, ideal for deployments spanning
// multiple regions or Kubernetes clusters where UDP multicast/broadcast is strictly firewalled.
type RedisHealth struct {
	rdb       redis.UniversalClient
	replicaID string
	tracker   *Tracker
	interval  time.Duration
	ttl       time.Duration
	targets   []router.Target

	mu   sync.Mutex
	stop chan struct{}
	done chan struct{}
}

// NewRedisHealth creates a new Redis-backed cross-replica health synchronizer.
func NewRedisHealth(rdb redis.UniversalClient, replicaID string, tracker *Tracker, targets []router.Target, interval time.Duration) *RedisHealth {
	if replicaID == "" {
		replicaID = uuid.NewString()
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &RedisHealth{
		rdb:       rdb,
		replicaID: replicaID,
		tracker:   tracker,
		interval:  interval,
		ttl:       interval * 3,
		targets:   append([]router.Target(nil), targets...),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// SetTargets updates the target list to synchronize.
func (rh *RedisHealth) SetTargets(targets []router.Target) {
	rh.mu.Lock()
	defer rh.mu.Unlock()
	rh.targets = append([]router.Target(nil), targets...)
}

// Start begins periodic publication and pulling of peer health metrics.
func (rh *RedisHealth) Start(ctx context.Context) {
	go rh.loop(ctx)
}

// Stop shuts down the background synchronization loop.
func (rh *RedisHealth) Stop() {
	rh.mu.Lock()
	select {
	case <-rh.stop:
		rh.mu.Unlock()
		return
	default:
		close(rh.stop)
	}
	rh.mu.Unlock()
	<-rh.done
}

// SyncOnce performs a single publish-and-pull synchronization cycle.
func (rh *RedisHealth) SyncOnce(ctx context.Context) error {
	rh.publishOnce(ctx)
	rh.pullOnce(ctx)
	return nil
}

func (rh *RedisHealth) loop(ctx context.Context) {
	defer close(rh.done)
	ticker := time.NewTicker(rh.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-rh.stop:
			return
		case <-ticker.C:
			_ = rh.SyncOnce(ctx)
		}
	}
}

func (rh *RedisHealth) publishOnce(ctx context.Context) {
	if rh.tracker == nil || rh.rdb == nil {
		return
	}
	rh.mu.Lock()
	targets := append([]router.Target(nil), rh.targets...)
	rh.mu.Unlock()

	snap := rh.tracker.Snapshot()
	now := time.Now()
	pipe := rh.rdb.Pipeline()
	hasItems := false

	for _, tg := range targets {
		s, ok := snap[tg.String()]
		if !ok {
			continue
		}
		p := peerHealthPayload{
			UpdatedAt: now,
			Stats:     s,
		}
		b, err := json.Marshal(p)
		if err != nil {
			continue
		}
		key := "health:" + tg.String()
		pipe.HSet(ctx, key, rh.replicaID, string(b))
		pipe.Expire(ctx, key, rh.ttl)
		hasItems = true
	}

	if hasItems {
		_, _ = pipe.Exec(ctx)
	}
}

func (rh *RedisHealth) pullOnce(ctx context.Context) {
	if rh.tracker == nil || rh.rdb == nil {
		return
	}
	rh.mu.Lock()
	targets := append([]router.Target(nil), rh.targets...)
	rh.mu.Unlock()

	for _, tg := range targets {
		key := "health:" + tg.String()
		fields, err := rh.rdb.HGetAll(ctx, key).Result()
		if err != nil || len(fields) == 0 {
			continue
		}
		for replica, raw := range fields {
			if replica == rh.replicaID {
				continue
			}
			var p peerHealthPayload
			if err := json.Unmarshal([]byte(raw), &p); err == nil && !p.UpdatedAt.IsZero() {
				// Clean up stale entries if peer has stopped publishing past the TTL
				if time.Since(p.UpdatedAt) > rh.ttl {
					_ = rh.rdb.HDel(ctx, key, replica).Err()
					continue
				}
				rh.tracker.Merge(tg, p.Stats)
				continue
			}
			// Backward compatibility: raw Stats payload
			var s Stats
			if err := json.Unmarshal([]byte(raw), &s); err == nil {
				rh.tracker.Merge(tg, s)
			}
		}
	}
}
