package proof

import (
	"context"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

type mockDeltasStore struct {
	deltas []float64
}

func (m *mockDeltasStore) RoutingDeltas(_ context.Context, _ string, _ time.Time) ([]float64, error) {
	return m.deltas, nil
}

type mockOverrideWriter struct {
	overrides []store.Override
}

func (m *mockOverrideWriter) SetOverride(_ context.Context, o store.Override) error {
	m.overrides = append(m.overrides, o)
	return nil
}

type mockSavingsStore struct {
	actual         int64
	counterfactual int64
	cheapShare     float64
	n              int
}

func (m *mockSavingsStore) RoutingSavings(_ context.Context, _ string, _ time.Time) (int64, int64, float64, int, error) {
	return m.actual, m.counterfactual, m.cheapShare, m.n, nil
}

func TestQualityMonitor_NoDegradation(t *testing.T) {
	ctx := context.Background()
	// 100 deltas centered around 0.0 (-0.02 to +0.02)
	var deltas []float64
	for i := 0; i < 100; i++ {
		deltas = append(deltas, float64(i%5-2)*0.01)
	}

	dStore := &mockDeltasStore{deltas: deltas}
	oWriter := &mockOverrideWriter{}

	res, err := MonitorQuality(ctx, dStore, oWriter, "chat", time.Time{}, 50, -0.05)
	require.NoError(t, err)
	require.False(t, res.RollbackNeeded)
	require.InDelta(t, 0.0, res.MeanDelta, 0.02)
	require.Greater(t, res.CIHigh, -0.05)
	require.Empty(t, oWriter.overrides)
}

func TestQualityMonitor_SignificantDegradation_Rollback(t *testing.T) {
	ctx := context.Background()
	// 100 deltas centered around -0.15 (-0.12 to -0.18)
	var deltas []float64
	for i := 0; i < 100; i++ {
		deltas = append(deltas, -0.15+float64(i%5-2)*0.01)
	}

	dStore := &mockDeltasStore{deltas: deltas}
	oWriter := &mockOverrideWriter{}

	res, err := MonitorQuality(ctx, dStore, oWriter, "chat", time.Time{}, 50, -0.05)
	require.NoError(t, err)
	require.True(t, res.RollbackNeeded)
	require.Less(t, res.CIHigh, -0.05)
	require.Contains(t, res.Message, "auto_rollback")

	// Verify override was written
	require.Len(t, oWriter.overrides, 1)
	ov := oWriter.overrides[0]
	require.Equal(t, "chat", ov.Route)
	require.Equal(t, "smart_route.mode", ov.Key)
	require.Equal(t, "off", ov.Value)
	require.Equal(t, "proofgate-monitor", ov.Actor)
}

func TestQualityMonitor_InsufficientData(t *testing.T) {
	ctx := context.Background()
	// 20 pairs (< 50)
	var deltas []float64
	for i := 0; i < 20; i++ {
		deltas = append(deltas, -0.15)
	}

	dStore := &mockDeltasStore{deltas: deltas}
	oWriter := &mockOverrideWriter{}

	res, err := MonitorQuality(ctx, dStore, oWriter, "chat", time.Time{}, 50, -0.05)
	require.NoError(t, err)
	require.False(t, res.RollbackNeeded)
	require.Contains(t, res.Message, "insufficient data (need >= 50, have 20)")
	require.Empty(t, oWriter.overrides)
}

func TestSavingsReport_Calculations(t *testing.T) {
	ctx := context.Background()
	// actual = 100_000 micros ($0.10)
	// counterfactual = 500_000 micros ($0.50)
	// savings = $0.40 (80%)
	sStore := &mockSavingsStore{
		actual:         100_000,
		counterfactual: 500_000,
		cheapShare:     0.75,
		n:              100,
	}

	rep, err := GenerateSavingsReport(ctx, sStore, nil, "chat", time.Time{})
	require.NoError(t, err)
	require.Equal(t, "chat", rep.Route)
	require.Equal(t, 100, rep.Requests)
	require.InDelta(t, 0.10, rep.ActualUSD, 1e-4)
	require.InDelta(t, 0.50, rep.CounterfactualUSD, 1e-4)
	require.InDelta(t, 0.40, rep.DollarsSavedUSD, 1e-4)
	require.InDelta(t, 80.0, rep.PercentSaved, 1e-2)
	require.InDelta(t, 0.75, rep.CheapShare, 1e-4)
}
