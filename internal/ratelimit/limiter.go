// Package ratelimit enforces per-tenant RPM and TPM with Redis token buckets.
package ratelimit

import (
	"context"
	"time"

	"github.com/proofgate/proofgate/internal/store"
	"github.com/redis/go-redis/v9"
)

type Decision struct {
	Allowed    bool
	TooLarge   bool
	RetryAfter time.Duration
}

type Backend interface {
	Take(ctx context.Context, tenantID string, p store.TenantPolicy, tokens int) (Decision, error)
	Adjust(ctx context.Context, tenantID string, p store.TenantPolicy, delta int) error
}

// takeScript checks both buckets and charges them only if both allow.
// KEYS: 1=request bucket, 2=token bucket. ARGV: req_cap, req_cost, tok_cap, tok_cost (cap 0 = unlimited).
// Buckets refill their full capacity every 60s. Returns {1,0} allowed, {0,wait_ms} denied, {-1,0} too large.
var takeScript = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local caps = {tonumber(ARGV[1]), tonumber(ARGV[3])}
local costs = {tonumber(ARGV[2]), tonumber(ARGV[4])}
local levels = {}
local wait = 0
for i = 1, 2 do
  if caps[i] > 0 then
    if costs[i] > caps[i] then return {-1, 0} end
    local rate = caps[i] / 60000
    local b = redis.call('HMGET', KEYS[i], 't', 'ts')
    local tokens = tonumber(b[1])
    if tokens == nil then
      tokens = caps[i]
    else
      tokens = math.min(caps[i], tokens + math.max(0, now - tonumber(b[2])) * rate)
    end
    levels[i] = tokens
    if tokens < costs[i] then
      local w = math.ceil((costs[i] - tokens) / rate)
      if w > wait then wait = w end
    end
  end
end
if wait > 0 then return {0, wait} end
for i = 1, 2 do
  if caps[i] > 0 then
    redis.call('HSET', KEYS[i], 't', levels[i] - costs[i], 'ts', now)
    redis.call('PEXPIRE', KEYS[i], 120000)
  end
end
return {1, 0}
`)

// adjustScript adds delta tokens (refund if positive, extra charge if negative; may go below zero,
// which delays the tenant's next requests instead of letting overuse go unpaid).
var adjustScript = redis.NewScript(`
local cap = tonumber(ARGV[1])
if cap <= 0 then return 0 end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local b = redis.call('HMGET', KEYS[1], 't', 'ts')
local tokens = tonumber(b[1])
if tokens == nil then
  tokens = cap
else
  tokens = tokens + math.max(0, now - tonumber(b[2])) * cap / 60000
end
tokens = math.min(cap, tokens + tonumber(ARGV[2]))
redis.call('HSET', KEYS[1], 't', tokens, 'ts', now)
redis.call('PEXPIRE', KEYS[1], 120000)
return 1
`)

type Redis struct {
	rdb redis.UniversalClient
}

func NewRedis(rdb redis.UniversalClient) *Redis { return &Redis{rdb: rdb} }

func keys(tenantID string) (string, string) {
	tag := "{t:" + tenantID + "}"
	return "rl:" + tag + ":req", "rl:" + tag + ":tok"
}

func (l *Redis) Take(ctx context.Context, tenantID string, p store.TenantPolicy, tokens int) (Decision, error) {
	rk, tk := keys(tenantID)
	res, err := takeScript.Run(ctx, l.rdb, []string{rk, tk}, p.RPM, 1, p.TPM, tokens).Int64Slice()
	if err != nil {
		return Decision{}, err
	}
	switch res[0] {
	case 1:
		return Decision{Allowed: true}, nil
	case -1:
		return Decision{TooLarge: true}, nil
	default:
		return Decision{RetryAfter: time.Duration(res[1]) * time.Millisecond}, nil
	}
}

func (l *Redis) Adjust(ctx context.Context, tenantID string, p store.TenantPolicy, delta int) error {
	if delta == 0 || p.TPM == 0 {
		return nil
	}
	_, tk := keys(tenantID)
	return adjustScript.Run(ctx, l.rdb, []string{tk}, p.TPM, delta).Err()
}
