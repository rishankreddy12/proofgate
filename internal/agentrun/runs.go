package agentrun

import (
	"context"

	"github.com/proofgate/proofgate/internal/store"
	"github.com/redis/go-redis/v9"
)

type StepStatus int

const (
	Allowed        StepStatus = 1
	StepsExceeded  StepStatus = -1
	CostExceeded   StepStatus = -2
	TokensExceeded StepStatus = -3
	Loop           StepStatus = -4
)

type StepResult struct {
	Status     StepStatus
	Steps      int
	CostMicros int64
	Tokens     int
	Repeats    int
}

type Store interface {
	Step(ctx context.Context, tenantID, runID string, p store.RunPolicy, fingerprint string, estCost int64, estTokens int) (StepResult, error)
	Charge(ctx context.Context, tenantID, runID string, p store.RunPolicy, actualMicros int64, actualTokens int, reservedMicros int64, reservedTokens int) error
}

// stepScript: KEYS 1=run hash, 2=fingerprint list.
// ARGV: max_steps, max_cost_micros, max_tokens, ttl_ms, fingerprint, loop_repeats, loop_window, est_cost, est_tokens
var stepScript = redis.NewScript(`
local steps = tonumber(redis.call('HGET', KEYS[1], 'steps') or '0')
local cost = tonumber(redis.call('HGET', KEYS[1], 'cost') or '0')
local tokens = tonumber(redis.call('HGET', KEYS[1], 'tokens') or '0')
local maxSteps, maxCost, maxTokens = tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3])
if maxSteps > 0 and steps >= maxSteps then return {-1, steps, cost, tokens, 0} end
if maxCost > 0 and cost >= maxCost then return {-2, steps, cost, tokens, 0} end
if maxTokens > 0 and tokens >= maxTokens then return {-3, steps, cost, tokens, 0} end
local repeats = 1
local window = tonumber(ARGV[7])
if ARGV[5] ~= '' and tonumber(ARGV[6]) > 0 then
  for _, v in ipairs(redis.call('LRANGE', KEYS[2], 0, window - 1)) do
    if v == ARGV[5] then repeats = repeats + 1 end
  end
  if repeats >= tonumber(ARGV[6]) then return {-4, steps, cost, tokens, repeats} end
  redis.call('LPUSH', KEYS[2], ARGV[5])
  redis.call('LTRIM', KEYS[2], 0, window - 1)
  redis.call('PEXPIRE', KEYS[2], ARGV[4])
end
redis.call('HINCRBY', KEYS[1], 'steps', 1)
redis.call('HINCRBY', KEYS[1], 'cost', tonumber(ARGV[8]))
redis.call('HINCRBY', KEYS[1], 'tokens', tonumber(ARGV[9]))
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return {1, steps + 1, cost, tokens, repeats}
`)

type RedisStore struct{ rdb redis.UniversalClient }

func NewRedisStore(rdb redis.UniversalClient) *RedisStore { return &RedisStore{rdb: rdb} }

func keys(tenantID, runID string) (string, string) {
	tag := "{t:" + tenantID + "}"
	return "run:" + tag + ":" + runID, "runfp:" + tag + ":" + runID
}

func (s *RedisStore) Step(ctx context.Context, tenantID, runID string, p store.RunPolicy, fp string, estCost int64, estTokens int) (StepResult, error) {
	rk, fk := keys(tenantID, runID)
	v, err := stepScript.Run(ctx, s.rdb, []string{rk, fk}, p.MaxSteps, p.CostMicros(), p.MaxTokens,
		p.TTL.Milliseconds(), fp, p.LoopRepeats, p.LoopWindow, estCost, estTokens).Int64Slice()
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{Status: StepStatus(v[0]), Steps: int(v[1]), CostMicros: v[2], Tokens: int(v[3]), Repeats: int(v[4])}, nil
}

func (s *RedisStore) Charge(ctx context.Context, tenantID, runID string, p store.RunPolicy, actualMicros int64, actualTokens int, reservedMicros int64, reservedTokens int) error {
	rk, _ := keys(tenantID, runID)
	pipe := s.rdb.TxPipeline()
	
	costDelta := actualMicros - reservedMicros
	tokensDelta := actualTokens - reservedTokens
	
	if costDelta != 0 {
		pipe.HIncrBy(ctx, rk, "cost", costDelta)
	}
	if tokensDelta != 0 {
		pipe.HIncrBy(ctx, rk, "tokens", int64(tokensDelta))
	}
	pipe.PExpire(ctx, rk, p.TTL)
	_, err := pipe.Exec(ctx)
	return err
}
