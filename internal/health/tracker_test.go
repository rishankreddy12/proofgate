package health

import (
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/stretchr/testify/require"
)

var tgt = router.Target{Provider: "a", Model: "m"}

func newTracker(now *time.Time) *Tracker {
	return NewTracker(config.HealthConfig{Alpha: 0.5, Breaches: 3, Recover: 30 * time.Second, MinSamples: 3},
		map[string]config.SLO{"a/m": {TTFTMs: 500, MinTPS: 20, MaxErrorRate: 0.5}}, func() time.Time { return *now })
}

func TestEWMAAndHedgeDelay(t *testing.T) {
	now := time.Unix(0, 0)
	tr := newTracker(&now)
	require.Equal(t, 300*time.Millisecond, tr.HedgeDelay(tgt, 300*time.Millisecond), "too few samples: default")
	for i := 0; i < 5; i++ {
		tr.Observe(Sample{Target: tgt, TTFT: 100 * time.Millisecond, Outcome: OK})
	}
	s := tr.Stats(tgt)
	require.InDelta(t, 100, s.TTFTMs, 1e-6)
	require.Equal(t, 5, s.TTFTN)
	require.Equal(t, 100*time.Millisecond, tr.HedgeDelay(tgt, time.Second), "no variance: delay = mean")
	tr.Observe(Sample{Target: tgt, Tokens: 100, Gen: 2 * time.Second})
	require.InDelta(t, 50, tr.Stats(tgt).TPS, 1e-6)
}

func TestDegradesAfterConsecutiveBreachesAndRecovers(t *testing.T) {
	now := time.Unix(0, 0)
	tr := newTracker(&now)
	for i := 0; i < 3; i++ {
		tr.Observe(Sample{Target: tgt, TTFT: 100 * time.Millisecond, Outcome: OK})
	}
	require.False(t, tr.Degraded(tgt))
	tr.Observe(Sample{Target: tgt, TTFT: 3 * time.Second, Outcome: OK}) // EWMA 1550ms: breach 1
	tr.Observe(Sample{Target: tgt, TTFT: 3 * time.Second, Outcome: OK}) // breach 2
	require.False(t, tr.Degraded(tgt), "hysteresis: two breaches are not enough")
	tr.Observe(Sample{Target: tgt, TTFT: 3 * time.Second, Outcome: OK}) // breach 3
	require.True(t, tr.Degraded(tgt))
	require.Contains(t, tr.Stats(tgt).Reason, "ttft")

	for i := 0; i < 6; i++ {
		tr.Observe(Sample{Target: tgt, TTFT: 50 * time.Millisecond, Outcome: OK}) // EWMA falls below 500ms
	}
	require.True(t, tr.Degraded(tgt), "must stay clean for the recovery period")
	now = now.Add(31 * time.Second)
	tr.Observe(Sample{Target: tgt, TTFT: 50 * time.Millisecond, Outcome: OK})
	require.False(t, tr.Degraded(tgt))
}

func TestErrorRateAndNoSLO(t *testing.T) {
	now := time.Unix(0, 0)
	tr := newTracker(&now)
	for i := 0; i < 6; i++ {
		tr.Observe(Sample{Target: tgt, Outcome: Failed})
	}
	require.True(t, tr.Degraded(tgt))
	other := router.Target{Provider: "b", Model: "x"}
	for i := 0; i < 10; i++ {
		tr.Observe(Sample{Target: other, TTFT: time.Minute, Outcome: Failed})
	}
	require.False(t, tr.Degraded(other), "targets without an SLO are never degraded by the tracker")
}
