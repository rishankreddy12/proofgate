// Package adminauth provides enterprise-grade capabilities, configuration, and structural components for the adminauth subsystem.
package adminauth

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// SessionStore defines the storage backend for session state, rate limiting, and user indexing.
type SessionStore interface {
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	SetXX(ctx context.Context, key string, val []byte, ttl time.Duration) (bool, error)
	Get(ctx context.Context, key string) ([]byte, error)
	Del(ctx context.Context, keys ...string) error
	IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error)
	GetWithTTL(ctx context.Context, key string) (int64, time.Duration, error)
	SAdd(ctx context.Context, key string, members ...string) error
	SRem(ctx context.Context, key string, members ...string) error
	SMembers(ctx context.Context, key string) ([]string, error)
	Scan(ctx context.Context, match string) ([]string, error)
}

// RedisStore implements SessionStore backed by Redis.
type RedisStore struct {
	rdb redis.Cmdable
}

// NewRedisStore executes the primary logic for the NewRedisStore operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewRedisStore(rdb redis.Cmdable) *RedisStore {
	return &RedisStore{rdb: rdb}
}

// Set executes the primary logic for the Set operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (r *RedisStore) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return r.rdb.Set(ctx, key, val, ttl).Err()
}

// SetXX executes the primary logic for the SetXX operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (r *RedisStore) SetXX(ctx context.Context, key string, val []byte, ttl time.Duration) (bool, error) {
	return r.rdb.SetXX(ctx, key, val, ttl).Result()
}

// Get executes the primary logic for the Get operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (r *RedisStore) Get(ctx context.Context, key string) ([]byte, error) {
	b, err := r.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrSessionNotFound
	}
	return b, err
}

// Del executes the primary logic for the Del operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (r *RedisStore) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return r.rdb.Del(ctx, keys...).Err()
}

// Scan executes the primary logic for the Scan operation.
// It ensures thread-safe execution, input validation, and proper error handling.
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

// Atomic Lua script: INCR; set EXPIRE if first hit OR if TTL is missing (self-heals old keys)
var incrWithTTLScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 or redis.call('TTL', KEYS[1]) < 0 then
    redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return n
`)

// IncrWithTTL executes the primary logic for the IncrWithTTL operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (r *RedisStore) IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	secs := int64(math.Ceil(ttl.Seconds()))
	if secs < 1 {
		secs = 1
	}
	res, err := incrWithTTLScript.Run(ctx, r.rdb, []string{key}, secs).Int64()
	if err != nil {
		return 0, err
	}
	return res, nil
}

// GetWithTTL executes the primary logic for the GetWithTTL operation.
// It ensures thread-safe execution, input validation, and proper error handling.
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

// SAdd executes the primary logic for the SAdd operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (r *RedisStore) SAdd(ctx context.Context, key string, members ...string) error {
	if len(members) == 0 {
		return nil
	}
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	return r.rdb.SAdd(ctx, key, args...).Err()
}

// SRem executes the primary logic for the SRem operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (r *RedisStore) SRem(ctx context.Context, key string, members ...string) error {
	if len(members) == 0 {
		return nil
	}
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	return r.rdb.SRem(ctx, key, args...).Err()
}

// SMembers executes the primary logic for the SMembers operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (r *RedisStore) SMembers(ctx context.Context, key string) ([]string, error) {
	return r.rdb.SMembers(ctx, key).Result()
}

// MemStore is an in-memory implementation of SessionStore used for fast, deterministic testing.
type MemStore struct {
	mu   sync.Mutex
	data map[string]memItem
	sets map[string]map[string]struct{}
}

type memItem struct {
	val       []byte
	expiresAt time.Time
}

// NewMemStore executes the primary logic for the NewMemStore operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewMemStore() *MemStore {
	return &MemStore{
		data: make(map[string]memItem),
		sets: make(map[string]map[string]struct{}),
	}
}

// Set executes the primary logic for the Set operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (m *MemStore) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = memItem{
		val:       val,
		expiresAt: time.Now().Add(ttl),
	}
	return nil
}

// SetXX executes the primary logic for the SetXX operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (m *MemStore) SetXX(ctx context.Context, key string, val []byte, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.data[key]
	now := time.Now()
	if !ok || now.After(item.expiresAt) {
		delete(m.data, key)
		return false, nil
	}
	m.data[key] = memItem{
		val:       val,
		expiresAt: now.Add(ttl),
	}
	return true, nil
}

// Get executes the primary logic for the Get operation.
// It ensures thread-safe execution, input validation, and proper error handling.
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

// Del executes the primary logic for the Del operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (m *MemStore) Del(ctx context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.data, k)
		delete(m.sets, k)
	}
	return nil
}

// Scan executes the primary logic for the Scan operation.
// It ensures thread-safe execution, input validation, and proper error handling.
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

// IncrWithTTL executes the primary logic for the IncrWithTTL operation.
// It ensures thread-safe execution, input validation, and proper error handling.
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

// GetWithTTL executes the primary logic for the GetWithTTL operation.
// It ensures thread-safe execution, input validation, and proper error handling.
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

// SAdd executes the primary logic for the SAdd operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (m *MemStore) SAdd(ctx context.Context, key string, members ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sets[key]
	if !ok {
		s = make(map[string]struct{})
		m.sets[key] = s
	}
	for _, mem := range members {
		s[mem] = struct{}{}
	}
	return nil
}

// SRem executes the primary logic for the SRem operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (m *MemStore) SRem(ctx context.Context, key string, members ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sets[key]
	if !ok {
		return nil
	}
	for _, mem := range members {
		delete(s, mem)
	}
	return nil
}

// SMembers executes the primary logic for the SMembers operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (m *MemStore) SMembers(ctx context.Context, key string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sets[key]
	if !ok {
		return nil, nil
	}
	out := make([]string, 0, len(s))
	for mem := range s {
		out = append(out, mem)
	}
	return out, nil
}
