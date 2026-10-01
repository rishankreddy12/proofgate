package budget

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/redis/go-redis/v9"
)

type Ledger interface {
	Spent(ctx context.Context, tenantID, month string) (int64, error)
	Add(ctx context.Context, tenantID, month string, micros int64) error
	Reserve(ctx context.Context, tenantID, month string, est, limit int64) (ok bool, spent int64, err error)
}

func Month(t time.Time) string { return t.UTC().Format("2006-01") }

// reserveScript atomically checks if (cur + est <= limit). If so, it increments by est
// and refreshes TTL. If not, it returns {0, cur} without modifying the key.
var reserveScript = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
local est = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
if cur + est > limit then
  return {0, cur}
end
local next = cur + est
redis.call('INCRBY', KEYS[1], ARGV[1])
redis.call('EXPIRE', KEYS[1], ttl)
return {1, next}
`)

// addScript adjusts spend by delta (which may be negative for refunds).
// It clamps the final value so the key never drops below 0.
var addScript = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
local delta = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
local next = cur + delta
if next < 0 then
  next = 0
end
redis.call('SET', KEYS[1], tostring(next))
redis.call('EXPIRE', KEYS[1], ttl)
return next
`)

type RedisLedger struct {
	rdb redis.UniversalClient
}

func NewRedisLedger(rdb redis.UniversalClient) *RedisLedger { return &RedisLedger{rdb: rdb} }

func ledgerKey(tenantID, month string) string { return "budget:{t:" + tenantID + "}:" + month }

func (l *RedisLedger) Spent(ctx context.Context, tenantID, month string) (int64, error) {
	v, err := l.rdb.Get(ctx, ledgerKey(tenantID, month)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return v, err
}

func (l *RedisLedger) Add(ctx context.Context, tenantID, month string, micros int64) error {
	k := ledgerKey(tenantID, month)
	ttlSecs := int64(40 * 24 * 3600)
	return addScript.Run(ctx, l.rdb, []string{k}, micros, ttlSecs).Err()
}

func (l *RedisLedger) Reserve(ctx context.Context, tenantID, month string, est, limit int64) (bool, int64, error) {
	k := ledgerKey(tenantID, month)
	ttlSecs := int64(40 * 24 * 3600)
	res, err := reserveScript.Run(ctx, l.rdb, []string{k}, est, limit, ttlSecs).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	if len(res) < 2 {
		return false, 0, fmt.Errorf("unexpected reserve result length: %d", len(res))
	}
	return res[0] == 1, res[1], nil
}

type StageOpts struct {
	Pricing        *Pricing
	ReserveStrict  bool
	RequirePricing bool
	DefaultMax     int
}

type Stage struct {
	l              Ledger
	pricing        *Pricing
	reserveStrict  bool
	requirePricing bool
	defaultMax     int
	now            func() time.Time
}

func NewStage(l Ledger, now func() time.Time, opts ...StageOpts) *Stage {
	s := &Stage{
		l:             l,
		reserveStrict: true,
		defaultMax:    8192,
		now:           now,
	}
	if len(opts) > 0 {
		opt := opts[0]
		s.pricing = opt.Pricing
		s.reserveStrict = opt.ReserveStrict
		s.requirePricing = opt.RequirePricing
		if opt.DefaultMax > 0 {
			s.defaultMax = opt.DefaultMax
		}
	}
	return s
}

func (s *Stage) Name() string { return "budget" }

func (s *Stage) Before(ctx context.Context, c *pipeline.Call) (bool, error) {
	limit := c.Principal.Tenant.BudgetMicros()
	if limit == 0 {
		return false, nil
	}

	month := Month(s.now())

	if s.reserveStrict {
		var targets []string
		if c.Target.Provider != "" && c.Target.Model != "" {
			targets = []string{c.Target.String()}
		} else if c.Route != nil {
			seen := make(map[string]bool)
			for _, t := range c.Route.Targets {
				ts := t.String()
				if !seen[ts] {
					seen[ts] = true
					targets = append(targets, ts)
				}
			}
			if c.Route.SmartRoute.Mode != "off" && c.Route.SmartRoute.Mode != "" {
				if c.Route.SmartRoute.CheapTarget != "" && !seen[c.Route.SmartRoute.CheapTarget] {
					seen[c.Route.SmartRoute.CheapTarget] = true
					targets = append(targets, c.Route.SmartRoute.CheapTarget)
				}
				if c.Route.SmartRoute.StrongTarget != "" && !seen[c.Route.SmartRoute.StrongTarget] {
					seen[c.Route.SmartRoute.StrongTarget] = true
					targets = append(targets, c.Route.SmartRoute.StrongTarget)
				}
			}
		} else if c.Request != nil && c.Request.Model != "" {
			targets = []string{c.Request.Model}
		}

		promptTokens := 0
		maxTokens := s.defaultMax
		if c.Request != nil {
			promptTokens = c.Request.EstimatePromptTokens()
			maxTokens = c.Request.EffectiveMaxTokens(s.defaultMax)
		}

		var est int64
		if s.pricing != nil && len(targets) > 0 {
			var ok bool
			est, ok = s.pricing.EstimateMaxMicros(targets, promptTokens, maxTokens)
			if !ok && s.requirePricing {
				return false, &api.Error{
					Status:  500,
					Message: fmt.Sprintf("unpriced target in route for tenant %s with monthly budget", c.Principal.TenantID),
					Type:    "api_error",
					Code:    "unpriced_target",
				}
			}
		}

		ok, spent, err := s.l.Reserve(ctx, c.Principal.TenantID, month, est, limit)
		if err != nil {
			if c.Principal.Tenant.Strict {
				return false, &api.Error{Status: 503, Message: "budget ledger unavailable", Type: "api_error", Code: "budget_unavailable"}
			}
			slog.Warn("budget ledger unavailable, failing open", "tenant", c.Principal.TenantID, "err", err)
			return false, nil
		}
		if !ok {
			c.Header.Set("X-ProofGate-Budget-Remaining-USD", fmt.Sprintf("%.6f", max(0, float64(limit-spent)/1e6)))
			return false, api.BudgetExceeded(fmt.Sprintf("monthly budget of $%.2f reached", float64(limit)/1e6))
		}
		c.Values["budget.reserved"] = est
		c.Header.Set("X-ProofGate-Budget-Remaining-USD", fmt.Sprintf("%.6f", float64(limit-spent)/1e6))
		return false, nil
	}

	// reserve: off (legacy read-then-spend)
	spent, err := s.l.Spent(ctx, c.Principal.TenantID, month)
	if err != nil {
		if c.Principal.Tenant.Strict {
			return false, &api.Error{Status: 503, Message: "budget ledger unavailable", Type: "api_error", Code: "budget_unavailable"}
		}
		slog.Warn("budget ledger unavailable, failing open", "tenant", c.Principal.TenantID, "err", err)
		return false, nil
	}
	if spent >= limit {
		return false, api.BudgetExceeded(fmt.Sprintf("monthly budget of $%.2f reached", float64(limit)/1e6))
	}
	c.Header.Set("X-ProofGate-Budget-Remaining-USD", fmt.Sprintf("%.6f", float64(limit-spent)/1e6))
	return false, nil
}

func (s *Stage) After(ctx context.Context, c *pipeline.Call) {
	reserved, hasReserved := c.Values["budget.reserved"].(int64)
	if hasReserved {
		delta := c.CostMicros - reserved
		if delta != 0 {
			if err := s.l.Add(context.WithoutCancel(ctx), c.Principal.TenantID, Month(s.now()), delta); err != nil {
				slog.Error("budget reconcile failed", "tenant", c.Principal.TenantID, "delta", delta, "err", err)
			}
		}
		return
	}
	if c.CostMicros <= 0 {
		return
	}
	if err := s.l.Add(context.WithoutCancel(ctx), c.Principal.TenantID, Month(s.now()), c.CostMicros); err != nil {
		slog.Error("budget charge failed", "tenant", c.Principal.TenantID, "micros", c.CostMicros, "err", err)
	}
}
