package proof

import (
	"context"
	"math"
	"time"

	"github.com/proofgate/proofgate/internal/stats"
)

type CurvePoint struct {
	Threshold    float64 `json:"threshold"`
	HitRate      float64 `json:"hit_rate"`
	FalseHitRate float64 `json:"false_hit_rate"`
	WilsonLower  float64 `json:"wilson_lower"`
	WilsonUpper  float64 `json:"wilson_upper"`
	SampleCount  int     `json:"n"`
}

type CurveInputStore interface {
	CurveInputs(ctx context.Context, route string, since time.Time) ([]float64, []LabeledPoint, error)
}

// TuneCacheFromData sweeps thresholds t from 0.70 to 0.98 in steps of 0.01,
// calculating hit rates and Wilson 95% upper bounds on the false-hit rate.
// It returns the lowest threshold where WilsonUpper <= targetFalseHitRate (or 0.98 if none satisfy).
func TuneCacheFromData(sims []float64, labeled []LabeledPoint, targetFalseHitRate float64) (float64, []CurvePoint) {
	if targetFalseHitRate <= 0 {
		targetFalseHitRate = 0.01
	}

	var curve []CurvePoint
	recommended := 0.98
	found := false

	totalQueries := len(sims)

	for step := 70; step <= 98; step++ {
		t := float64(step) / 100.0

		hits := 0
		for _, s := range sims {
			if s >= t {
				hits++
			}
		}
		hitRate := 0.0
		if totalQueries > 0 {
			hitRate = float64(hits) / float64(totalQueries)
		}

		nLabeled := 0
		falseHits := 0
		for _, pt := range labeled {
			if pt.Similarity >= t {
				nLabeled++
				if !pt.Acceptable {
					falseHits++
				}
			}
		}

		var fhr, lo, hi float64
		if nLabeled > 0 {
			fhr = float64(falseHits) / float64(nLabeled)
			lo, hi = stats.Wilson(falseHits, nLabeled, 1.96)
		} else {
			lo, hi = 0.0, 1.0
		}

		pt := CurvePoint{
			Threshold:    t,
			HitRate:      math.Round(hitRate*10000) / 10000,
			FalseHitRate: math.Round(fhr*10000) / 10000,
			WilsonLower:  math.Round(lo*10000) / 10000,
			WilsonUpper:  math.Round(hi*10000) / 10000,
			SampleCount:  nLabeled,
		}
		curve = append(curve, pt)

		if !found && nLabeled > 0 && hi <= targetFalseHitRate {
			recommended = t
			found = true
		}
	}

	return recommended, curve
}

func TuneCache(ctx context.Context, store CurveInputStore, route string, targetFalseHitRate float64, since time.Time) (float64, []CurvePoint, int, error) {
	if since.IsZero() {
		since = time.Now().Add(-14 * 24 * time.Hour)
	}
	sims, labeled, err := store.CurveInputs(ctx, route, since)
	if err != nil {
		return 0.98, nil, 0, err
	}
	recommended, curve := TuneCacheFromData(sims, labeled, targetFalseHitRate)
	return recommended, curve, len(labeled), nil
}
