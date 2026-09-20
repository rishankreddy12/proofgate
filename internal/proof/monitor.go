package proof

import (
	"context"
	"fmt"
	"time"

	"github.com/proofgate/proofgate/internal/stats"
	"github.com/proofgate/proofgate/internal/store"
)

type RoutingDeltasStore interface {
	RoutingDeltas(ctx context.Context, route string, since time.Time) ([]float64, error)
}

type OverrideWriter interface {
	SetOverride(ctx context.Context, o store.Override) error
}

type MonitorResult struct {
	MeanDelta      float64
	CILow          float64
	CIHigh         float64
	Pairs          int
	RollbackNeeded bool
	Message        string
}

func MonitorQuality(
	ctx context.Context,
	deltasStore RoutingDeltasStore,
	overrides OverrideWriter,
	route string,
	since time.Time,
	minPairs int,
	rollbackThreshold float64,
) (MonitorResult, error) {
	if minPairs <= 0 {
		minPairs = 50
	}
	if rollbackThreshold == 0 {
		rollbackThreshold = -0.05
	}
	if since.IsZero() {
		since = time.Now().Add(-7 * 24 * time.Hour)
	}

	deltas, err := deltasStore.RoutingDeltas(ctx, route, since)
	if err != nil {
		return MonitorResult{}, err
	}

	n := len(deltas)
	if n < minPairs {
		msg := fmt.Sprintf("insufficient data (need >= %d, have %d)", minPairs, n)
		return MonitorResult{
			Pairs:          n,
			RollbackNeeded: false,
			Message:        msg,
		}, nil
	}

	mean, ciLow, ciHigh := stats.BootstrapMeanCI(deltas, 2000, 0.05, 42)

	res := MonitorResult{
		MeanDelta: mean,
		CILow:     ciLow,
		CIHigh:    ciHigh,
		Pairs:     n,
	}

	if ciHigh < rollbackThreshold {
		res.RollbackNeeded = true
		res.Message = fmt.Sprintf("auto_rollback: quality delta CI [%.3f, %.3f] below %.2f", ciLow, ciHigh, rollbackThreshold)

		if overrides != nil {
			err := overrides.SetOverride(ctx, store.Override{
				Route:  route,
				Key:    "smart_route.mode",
				Value:  "off",
				Reason: res.Message,
				Actor:  "proofgate-monitor",
			})
			if err != nil {
				return res, fmt.Errorf("write rollback override: %w", err)
			}
		}
	}

	return res, nil
}
