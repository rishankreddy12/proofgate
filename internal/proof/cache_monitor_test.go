package proof

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mockCacheLabelsStore struct {
	total        int
	unacceptable int
	err          error
}

func (m *mockCacheLabelsStore) CacheErrorRate(_ context.Context, _ string, _ time.Time) (int, int, error) {
	if m.err != nil {
		return 0, 0, m.err
	}
	return m.total, m.unacceptable, nil
}

func TestCacheMonitor_NoDegradation(t *testing.T) {
	ctx := context.Background()
	labelsStore := &mockCacheLabelsStore{total: 200, unacceptable: 0}
	oWriter := &mockOverrideWriter{}

	res, err := MonitorCacheQuality(ctx, labelsStore, oWriter, "faq", time.Time{}, 30, 0.02)
	require.NoError(t, err)
	require.False(t, res.RollbackNeeded)
	require.Equal(t, 0.0, res.ObservedRate)
	require.Empty(t, oWriter.overrides)
}

func TestCacheMonitor_HighFalseHitRate_Rollback(t *testing.T) {
	ctx := context.Background()
	// 50 evaluations, 5 unacceptable (10% false hit rate)
	labelsStore := &mockCacheLabelsStore{total: 50, unacceptable: 5}
	oWriter := &mockOverrideWriter{}

	res, err := MonitorCacheQuality(ctx, labelsStore, oWriter, "faq", time.Time{}, 30, 0.02)
	require.NoError(t, err)
	require.True(t, res.RollbackNeeded)
	require.Greater(t, res.WilsonHigh, 0.02)
	require.Contains(t, res.Message, "auto_rollback: cache false-hit")

	require.Len(t, oWriter.overrides, 1)
	ov := oWriter.overrides[0]
	require.Equal(t, "faq", ov.Route)
	require.Equal(t, "cache.mode", ov.Key)
	require.Equal(t, "shadow", ov.Value)
	require.Equal(t, "proofgate-monitor", ov.Actor)
}

func TestCacheMonitor_InsufficientData(t *testing.T) {
	ctx := context.Background()
	labelsStore := &mockCacheLabelsStore{total: 15, unacceptable: 3}
	oWriter := &mockOverrideWriter{}

	res, err := MonitorCacheQuality(ctx, labelsStore, oWriter, "faq", time.Time{}, 30, 0.02)
	require.NoError(t, err)
	require.False(t, res.RollbackNeeded)
	require.Contains(t, res.Message, "insufficient evaluated cache hits")
	require.Empty(t, oWriter.overrides)
}
