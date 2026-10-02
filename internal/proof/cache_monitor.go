package proof

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/proofgate/proofgate/internal/stats"
	"github.com/proofgate/proofgate/internal/store"
)

type CacheLabelsStore interface {
	CacheErrorRate(ctx context.Context, route string, since time.Time) (total int, unacceptable int, err error)
}

type CacheMonitorResult struct {
	TotalEvaluated int
	Unacceptable   int
	ObservedRate   float64
	WilsonHigh     float64
	RollbackNeeded bool
	Message        string
}

func MonitorCacheQuality(
	ctx context.Context,
	labelsStore CacheLabelsStore,
	overrides OverrideWriter,
	route string,
	since time.Time,
	minEvaluated int,
	maxErrorRate float64,
) (CacheMonitorResult, error) {
	if minEvaluated <= 0 {
		minEvaluated = 30
	}
	if maxErrorRate <= 0 {
		maxErrorRate = 0.02 // 2% maximum allowed false hit rate
	}
	if since.IsZero() {
		since = time.Now().Add(-24 * time.Hour)
	}

	total, bad, err := labelsStore.CacheErrorRate(ctx, route, since)
	if err != nil {
		return CacheMonitorResult{}, err
	}

	if total < minEvaluated {
		return CacheMonitorResult{
			TotalEvaluated: total,
			Unacceptable:   bad,
			RollbackNeeded: false,
			Message:        fmt.Sprintf("insufficient evaluated cache hits (need >= %d, have %d)", minEvaluated, total),
		}, nil
	}

	_, hi := stats.Wilson(bad, total, 1.96)
	observed := float64(bad) / float64(total)

	res := CacheMonitorResult{
		TotalEvaluated: total,
		Unacceptable:   bad,
		ObservedRate:   observed,
		WilsonHigh:     hi,
	}

	if hi > maxErrorRate {
		res.RollbackNeeded = true
		res.Message = fmt.Sprintf("auto_rollback: cache false-hit Wilson 95%% upper bound [%.3f] exceeds max acceptable [%.3f]", hi, maxErrorRate)

		if overrides != nil {
			err := overrides.SetOverride(ctx, store.Override{
				Route:  route,
				Key:    "cache.mode",
				Value:  "shadow",
				Reason: res.Message,
				Actor:  "proofgate-monitor",
			})
			if err != nil {
				return res, fmt.Errorf("write cache rollback override: %w", err)
			}
			RollbackTotal.WithLabelValues(route, "cache_false_hit_regression").Inc()
			slog.Warn("cache monitor executed automated rollback to shadow",
				"route", route,
				"wilson_high", hi,
				"observed_rate", observed,
				"max_error_rate", maxErrorRate,
				"total", total,
				"unacceptable", bad,
				"reason", res.Message,
			)
		}
	}

	return res, nil
}
