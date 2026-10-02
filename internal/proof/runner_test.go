package proof

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRunMonitor_LeaderGatedExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var nonLeaderRuns atomic.Int32
	var leaderRuns atomic.Int32

	// Instance 1: Non-leader
	lNon := &staticLeader{leader: false}
	go RunMonitor(ctx, lNon, 10*time.Millisecond, func(_ context.Context) error {
		nonLeaderRuns.Add(1)
		return nil
	})

	// Instance 2: Leader
	lLead := &staticLeader{leader: true}
	go RunMonitor(ctx, lLead, 10*time.Millisecond, func(_ context.Context) error {
		leaderRuns.Add(1)
		return nil
	})

	time.Sleep(50 * time.Millisecond)
	cancel()

	require.EqualValues(t, 0, nonLeaderRuns.Load(), "non-leader must never execute the monitoring loop")
	require.GreaterOrEqual(t, leaderRuns.Load(), int32(2), "leader must execute the monitoring loop periodically")
}

func TestMonitorAllRoutes_AutomatedRollback(t *testing.T) {
	ctx := context.Background()

	routes := []config.RouteConfig{
		{
			Name:       "sr-route",
			SmartRoute: config.SmartRouteConfig{Mode: "on"},
		},
		{
			Name:  "cache-route",
			Cache: config.CacheConfig{Mode: "on"},
		},
		{
			Name:       "already-off",
			SmartRoute: config.SmartRouteConfig{Mode: "off"},
			Cache:      config.CacheConfig{Mode: "off"},
		},
	}

	// Degraded smart route data
	var badDeltas []float64
	for i := 0; i < 60; i++ {
		badDeltas = append(badDeltas, -0.20)
	}
	deltasStore := &mockDeltasStore{deltas: badDeltas}

	// Degraded cache false-hit data
	labelsStore := &mockCacheLabelsStore{total: 40, unacceptable: 8} // 20% false hit

	oWriter := &mockOverrideWriter{}
	cfg := config.ProofConfig{
		MinPairs:          50,
		RollbackThreshold: -0.05,
	}

	err := MonitorAllRoutes(ctx, routes, deltasStore, labelsStore, oWriter, cfg)
	require.NoError(t, err)

	require.Len(t, oWriter.overrides, 2)

	// Check smart route rollback
	var foundSR, foundCache bool
	for _, o := range oWriter.overrides {
		if o.Route == "sr-route" && o.Key == "smart_route.mode" && o.Value == "off" {
			foundSR = true
		}
		if o.Route == "cache-route" && o.Key == "cache.mode" && o.Value == "shadow" {
			foundCache = true
		}
	}
	require.True(t, foundSR, "expected smart_route.mode: off override for sr-route")
	require.True(t, foundCache, "expected cache.mode: shadow override for cache-route")
}
