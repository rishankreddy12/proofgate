package proof

import (
	"context"
	"math"
	"time"
)

type SavingsStore interface {
	RoutingSavings(ctx context.Context, route string, since time.Time) (actual, counterfactual int64, cheapShare float64, n int, err error)
}

type SavingsReport struct {
	Route             string  `json:"route"`
	Requests          int     `json:"requests"`
	ActualUSD         float64 `json:"actual_usd"`
	CounterfactualUSD float64 `json:"counterfactual_usd"`
	DollarsSavedUSD   float64 `json:"dollars_saved_usd"`
	PercentSaved      float64 `json:"percent_saved"`
	CheapShare        float64 `json:"cheap_share"`
	MeanDelta         float64 `json:"mean_delta,omitempty"`
	CILow             float64 `json:"ci_low,omitempty"`
	CIHigh            float64 `json:"ci_high,omitempty"`
}

func GenerateSavingsReport(ctx context.Context, store SavingsStore, deltasStore RoutingDeltasStore, route string, since time.Time) (SavingsReport, error) {
	if since.IsZero() {
		since = time.Now().Add(-30 * 24 * time.Hour)
	}

	actual, counterfactual, cheapShare, n, err := store.RoutingSavings(ctx, route, since)
	if err != nil {
		return SavingsReport{}, err
	}

	actualUSD := float64(actual) / 1e6
	counterfactualUSD := float64(counterfactual) / 1e6
	savedUSD := float64(counterfactual-actual) / 1e6

	var pctSaved float64
	if counterfactual > 0 {
		pctSaved = float64(counterfactual-actual) / float64(counterfactual) * 100.0
	}

	rep := SavingsReport{
		Route:             route,
		Requests:          n,
		ActualUSD:         math.Round(actualUSD*10000) / 10000,
		CounterfactualUSD: math.Round(counterfactualUSD*10000) / 10000,
		DollarsSavedUSD:   math.Round(savedUSD*10000) / 10000,
		PercentSaved:      math.Round(pctSaved*100) / 100,
		CheapShare:        math.Round(cheapShare*10000) / 10000,
	}

	if deltasStore != nil {
		res, err := MonitorQuality(ctx, deltasStore, nil, route, since, 10, -0.05)
		if err == nil && res.Pairs > 0 {
			rep.MeanDelta = math.Round(res.MeanDelta*10000) / 10000
			rep.CILow = math.Round(res.CILow*10000) / 10000
			rep.CIHigh = math.Round(res.CIHigh*10000) / 10000
		}
	}

	return rep, nil
}
