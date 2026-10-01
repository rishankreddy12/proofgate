package adminauth

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// SessionStore defines the storage backend for session state and rate limiting.
type SessionStore interface {
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Get(ctx context.Context, key string) ([]byte, error)
	Del(ctx context.Context, keys ...string) error
	Scan(ctx context.Context, match string) ([]string, error)
	IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error)
	GetWithTTL(ctx context.Context, key string) (int64, time.Duration, error)
}

// RedisStore implements SessionStore backed by Redis.
type RedisStore struct {
	rdb redis.Cmdable
}

func NewRedisStore(rdb redis.Cmdable) *RedisStore {
	return &RedisStore{rdb: rdb}
}

func (r *RedisStore) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return r.rdb.Set(ctx, key, val, ttl).Err()
}

func (r *RedisStore) Get(ctx context.Context, key string) ([]byte, error) {
	b, err := r.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrSessionNotFound
	}
	return b, err
}

func (r *RedisStore) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return r.rdb.Del(ctx, keys...).Err()
}

func (r *RedisStore) Scan(ctx context.Context, match string) ([]string, error) {
	var cursor uint64
	var result []string
	for {
		keys, nextCursor, err := r.rdb.Scan(ctx, cursor, match, 100).Result()
		if err != nil {
			return nil, err
		}
		result = append(result, keys...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return result, nil
}

func (r *RedisStore) IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	val, err := r.rdb.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	if val == 1 {
		_ = r.rdb.Expire(ctx, key, ttl).Err()
	}
	return val, nil
}

func (r *RedisStore) GetWithTTL(ctx context.Context, key string) (int64, time.Duration, error) {
	val, err := r.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	n, _ := strconv.ParseInt(val, 10, 64)
	ttl, err := r.rdb.TTL(ctx, key).Result()
	if err != nil || ttl < 0 {
		ttl = 0
	}
	return n, ttl, nil
}

// MemStore is an in-memory implementation of SessionStore used for fast, deterministic unit testing.
type MemStore struct {
	mu   sync.Mutex
	data map[string]memItem
}

type memItem struct {
	val       []byte
	expiresAt time.Time
}

func NewMemStore() *MemStore {
	return &MemStore{
		data: make(map[string]memItem),
	}
}

func (m *MemStore) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = memItem{
		val:       val,
		expiresAt: time.Now().Add(ttl),
	}
	return nil
}

func (m *MemStore) Get(ctx context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.data[key]
	if !ok || time.Now().After(item.expiresAt) {
		delete(m.data, key)
		return nil, ErrSessionNotFound
	}
	return item.val, nil
}

func (m *MemStore) Del(ctx context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.data, k)
	}
	return nil
}

func (m *MemStore) Scan(ctx context.Context, match string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := strings.TrimSuffix(match, "*")
	var keys []string
	now := time.Now()
	for k, item := range m.data {
		if now.After(item.expiresAt) {
			delete(m.data, k)
			continue
		}
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (m *MemStore) IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.data[key]
	now := time.Now()
	var count int64
	if ok && now.Before(item.expiresAt) {
		c, _ := strconv.ParseInt(string(item.val), 10, 64)
		count = c + 1
		m.data[key] = memItem{
			val:       []byte(strconv.FormatInt(count, 10)),
			expiresAt: item.expiresAt,
		}
	} else {
		count = 1
		m.data[key] = memItem{
			val:       []byte("1"),
			expiresAt: now.Add(ttl),
		}
	}
	return count, nil
}

func (m *MemStore) GetWithTTL(ctx context.Context, key string) (int64, time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.data[key]
	now := time.Now()
	if !ok || now.After(item.expiresAt) {
		delete(m.data, key)
		return 0, 0, nil
	}
	c, _ := strconv.ParseInt(string(item.val), 10, 64)
	return c, item.expiresAt.Sub(now), nil
}
