package proof

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/proofgate/proofgate/internal/config"
)

// RunMonitor runs the monitoring loop periodically when this instance is leader.
func RunMonitor(ctx context.Context, leader LeaderElection, every time.Duration, run func(context.Context) error) {
	if every <= 0 {
		every = 60 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if leader == nil || leader.IsLeader() {
				if err := run(ctx); err != nil {
					slog.Error("proof monitor error", "err", err)
				}
			}
		}
	}
}

// MonitorAllRoutes evaluates quality across all configured routes that have smart routing or caching.
func MonitorAllRoutes(
	ctx context.Context,
	routes []config.RouteConfig,
	deltas RoutingDeltasStore,
	labels CacheLabelsStore,
	overrides OverrideWriter,
	cfg config.ProofConfig,
) error {
	var errs []error
	for _, r := range routes {
		// 1. Smart route quality monitoring
		if r.SmartRoute.Mode == "on" && deltas != nil {
			res, err := MonitorQuality(ctx, deltas, overrides, r.Name, time.Time{}, cfg.MinPairs, cfg.RollbackThreshold)
			if err != nil {
				errs = append(errs, fmt.Errorf("route %s smart_route monitor: %w", r.Name, err))
			} else if res.RollbackNeeded {
				slog.Warn("smart_route auto-rollback executed", "route", r.Name, "message", res.Message)
			}
		}

		// 2. Cache false-hit monitoring
		if r.Cache.Mode == "on" && labels != nil {
			res, err := MonitorCacheQuality(ctx, labels, overrides, r.Name, time.Time{}, 30, 0.02)
			if err != nil {
				errs = append(errs, fmt.Errorf("route %s cache monitor: %w", r.Name, err))
			} else if res.RollbackNeeded {
				slog.Warn("cache auto-rollback executed", "route", r.Name, "message", res.Message)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("monitor errors: %v", errs)
	}
	return nil
}
