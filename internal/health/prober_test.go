package health

import (
	"context"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/router"
	"github.com/stretchr/testify/require"
)

func TestProberFeedsDegradedTargets(t *testing.T) {
	now := time.Unix(0, 0)
	tr := newTracker(&now)
	for i := 0; i < 6; i++ {
		tr.Observe(Sample{Target: tgt, Outcome: Failed})
	}
	require.True(t, tr.Degraded(tgt))
	var probed []router.Target
	p := NewProber(tr, func(_ context.Context, t router.Target) (time.Duration, error) {
		probed = append(probed, t)
		return 40 * time.Millisecond, nil
	}, time.Hour)
	for i := 0; i < 8; i++ {
		p.Tick(context.Background())
	}
	require.Len(t, probed, 8)
	now = now.Add(31 * time.Second)
	p.Tick(context.Background())
	require.False(t, tr.Degraded(tgt), "successful probes bring the target back")
}
