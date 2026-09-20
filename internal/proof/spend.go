package proof

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

type SpendGuard interface {
	CanSpend(ctx context.Context, estimatedMicros int64) (bool, error)
	AddSpend(ctx context.Context, micros int64) error
	SpentToday(ctx context.Context) (int64, error)
	Limit() int64
}

type ShadowSpendGuard struct {
	rdb         redis.UniversalClient
	dailyLimit  int64
	nowFn       func() time.Time
}

func NewShadowSpendGuard(rdb redis.UniversalClient, dailyLimitMicros int64) *ShadowSpendGuard {
	if dailyLimitMicros <= 0 {
		dailyLimitMicros = 5_000_000 // $5.00 default
	}
	return &ShadowSpendGuard{
		rdb:        rdb,
		dailyLimit: dailyLimitMicros,
		nowFn:      time.Now,
	}
}

func (s *ShadowSpendGuard) SetNow(fn func() time.Time) {
	if fn != nil {
		s.nowFn = fn
	}
}

func (s *ShadowSpendGuard) Limit() int64 {
	return s.dailyLimit
}

func (s *ShadowSpendGuard) todayKey() string {
	date := s.nowFn().UTC().Format("2006-01-02")
	return "proof:spend:" + date
}

func (s *ShadowSpendGuard) SpentToday(ctx context.Context) (int64, error) {
	if s.rdb == nil {
		return 0, nil
	}
	v, err := s.rdb.Get(ctx, s.todayKey()).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return v, err
}

func (s *ShadowSpendGuard) CanSpend(ctx context.Context, estimatedMicros int64) (bool, error) {
	spent, err := s.SpentToday(ctx)
	if err != nil {
		return false, err
	}
	return spent+estimatedMicros <= s.dailyLimit, nil
}

func (s *ShadowSpendGuard) AddSpend(ctx context.Context, micros int64) error {
	if s.rdb == nil || micros <= 0 {
		return nil
	}
	k := s.todayKey()
	pipe := s.rdb.TxPipeline()
	pipe.IncrBy(ctx, k, micros)
	pipe.Expire(ctx, k, 48*time.Hour)
	_, err := pipe.Exec(ctx)
	return err
}
