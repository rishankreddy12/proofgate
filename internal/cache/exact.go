package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

type Exact struct {
	rdb redis.UniversalClient
}

func NewExact(rdb redis.UniversalClient) *Exact { return &Exact{rdb: rdb} }

func (x *Exact) Get(ctx context.Context, key string) (*Entry, error) {
	b, err := x.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
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
