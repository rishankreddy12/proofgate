// Package cache provides enterprise-grade capabilities, configuration, and structural components for the cache subsystem.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Exact abstracts the storage engine (Redis) for 1:1 deterministic caching of LLM requests.
type Exact struct {
	rdb redis.UniversalClient
}

// NewExact initializes the Redis-backed Exact Match Cache.
func NewExact(rdb redis.UniversalClient) *Exact { return &Exact{rdb: rdb} }

// Get retrieves and deserializes a cached Entry based on its deterministic SHA-256 key.
func (x *Exact) Get(ctx context.Context, key string) (*Entry, error) {
	b, err := x.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil // Cache Miss
	}
	if err != nil {
		return nil, err
	}
	var e Entry
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// Put serializes and stores an Entry into Redis.
//
// Architecture: It utilizes a Redis Pipeline to atomically store the JSON payload and
// register the key into any associated tagging sets (e.g., for targeted purging).
// Because of the `{t:tenantID}` hash tag, this pipeline is guaranteed to succeed in a Redis Cluster.
func (x *Exact) Put(ctx context.Context, tenantID, key string, e Entry, ttl time.Duration) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	pipe := x.rdb.TxPipeline() // all keys share the tenant hash tag, so this is valid on a cluster
	pipe.Set(ctx, key, b, ttl)

	for _, t := range e.Tags {
		tk := TagKey(tenantID, t)
		pipe.SAdd(ctx, tk, key)
		pipe.Expire(ctx, tk, ttl)
	}
	_, err = pipe.Exec(ctx)
	return err
}
