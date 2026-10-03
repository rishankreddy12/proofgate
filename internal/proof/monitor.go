// Package proof provides enterprise-grade capabilities, configuration, and structural components for the proof subsystem.
package proof

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/proofgate/proofgate/internal/stats"
	"github.com/proofgate/proofgate/internal/store"
)

// RoutingDeltasStore defines the core enterprise configuration and state for RoutingDeltasStore.
// It is responsible for managing the lifecycle, validation, and schema of the RoutingDeltasStore entity.
type RoutingDeltasStore interface {
	RoutingDeltas(ctx context.Context, route string, since time.Time) ([]float64, error)
}

// OverrideWriter defines the core enterprise configuration and state for OverrideWriter.
// It is responsible for managing the lifecycle, validation, and schema of the OverrideWriter entity.
type OverrideWriter interface {
	SetOverride(ctx context.Context, o store.Override) error
}

// MonitorResult defines the core enterprise configuration and state for MonitorResult.
// It is responsible for managing the lifecycle, validation, and schema of the MonitorResult entity.
type MonitorResult struct {
	MeanDelta      float64
	CILow          float64
	CIHigh         float64
	Pairs          int
	RollbackNeeded bool
	Message        string
}

// MonitorQuality executes the primary logic for the MonitorQuality operation.
// It ensures thread-safe execution, input validation, and proper error handling.
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
			RollbackTotal.WithLabelValues(route, "quality_regression").Inc()
			slog.Warn("proof monitor executed automated rollback",
				"route", route,
				"ci_low", ciLow,
				"ci_high", ciHigh,
				"mean", mean,
				"threshold", rollbackThreshold,
				"pairs", n,
				"reason", res.Message,
			)
		}
	}

	return res, nil
}
