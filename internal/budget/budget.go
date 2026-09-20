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
}

func Month(t time.Time) string { return t.UTC().Format("2006-01") }

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
	pipe := l.rdb.TxPipeline()
	pipe.IncrBy(ctx, k, micros)
	pipe.Expire(ctx, k, 40*24*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}

type Stage struct {
	l   Ledger
	now func() time.Time
}

func NewStage(l Ledger, now func() time.Time) *Stage { return &Stage{l: l, now: now} }

func (s *Stage) Name() string { return "budget" }

func (s *Stage) Before(ctx context.Context, c *pipeline.Call) (bool, error) {
	limit := c.Principal.Tenant.BudgetMicros()
	if limit == 0 {
		return false, nil
	}
	spent, err := s.l.Spent(ctx, c.Principal.TenantID, Month(s.now()))
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
	if c.CostMicros <= 0 {
		return
	}
	if err := s.l.Add(context.WithoutCancel(ctx), c.Principal.TenantID, Month(s.now()), c.CostMicros); err != nil {
		slog.Error("budget charge failed", "tenant", c.Principal.TenantID, "micros", c.CostMicros, "err", err)
	}
}
