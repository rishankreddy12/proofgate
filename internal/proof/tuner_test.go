package proof

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTuneCache_SyntheticData(t *testing.T) {
	var sims []float64
	var labeled []LabeledPoint

	// 1. Generate synthetic data:
	// Below 0.85: high false hits
	for i := 0; i < 200; i++ {
		sim := 0.70 + float64(i)*0.0007 // 0.70 .. 0.84
		sims = append(sims, sim)
		labeled = append(labeled, LabeledPoint{
			Similarity: sim,
			Acceptable: i%2 == 0, // 50% unacceptable -> high false hit rate
			Rater:      "judge",
		})
	}

	// 0.85 .. 0.89: some false hits (10 false hits out of 100)
	for i := 0; i < 100; i++ {
		sim := 0.85 + float64(i)*0.0004 // 0.85 .. 0.89
		sims = append(sims, sim)
		labeled = append(labeled, LabeledPoint{
			Similarity: sim,
			Acceptable: i >= 10, // 10 false hits
			Rater:      "judge",
		})
	}

	// 0.90 .. 0.98: perfect accuracy (500 items, 0 false hits)
	for i := 0; i < 500; i++ {
		sim := 0.90 + float64(i)*0.00015 // 0.90 .. 0.975
		sims = append(sims, sim)
		labeled = append(labeled, LabeledPoint{
			Similarity: sim,
			Acceptable: true, // 100% acceptable -> 0 false hits
			Rater:      "judge",
		})
	}

	recommended, curve := TuneCacheFromData(sims, labeled, 0.01)

	// Curve points span 0.70 to 0.98 (29 points)
	require.Len(t, curve, 29)
	require.Equal(t, 0.70, curve[0].Threshold)
	require.Equal(t, 0.98, curve[28].Threshold)

	// Asserts monotonicity: false-hit rate decreases (or is non-increasing) as threshold increases
	for i := 1; i < len(curve); i++ {
		require.LessOrEqual(t, curve[i].FalseHitRate, curve[i-1].FalseHitRate+1e-6,
			"false-hit rate must be non-increasing with higher threshold")
	}

	// Asserts recommended threshold chooses lowest t where Wilson upper <= 0.01
	require.Equal(t, 0.86, recommended)

	// Check the point at the recommended threshold
	for _, pt := range curve {
		if pt.Threshold == recommended {
			require.LessOrEqual(t, pt.WilsonUpper, 0.01)
			require.Greater(t, pt.SampleCount, 0)
			break
		}
	}
}

func TestTuneCache_InsufficientDataFallsBackToConservative(t *testing.T) {
	// Zero labeled data
	recommended, curve := TuneCacheFromData([]float64{0.75, 0.85}, nil, 0.01)
	require.Equal(t, 0.98, recommended, "insufficient data should fall back to 0.98")
	require.Len(t, curve, 29)
}
