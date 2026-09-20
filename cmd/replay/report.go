package main

import (
	"sort"
	"time"
)

type Result struct {
	ReqID   string
	Phase   int
	Group   string
	Status  int
	Cache   string
	Source  string
	Latency time.Duration
}

type Report struct {
	Label          string  `json:"label"`
	Dataset        string  `json:"dataset"`
	Route          string  `json:"route"`
	Date           string  `json:"date"`
	Requests       int     `json:"requests"`
	Errors         int     `json:"errors"`
	Phase2Requests int     `json:"phase2_requests"`
	HitsExact      int     `json:"hits_exact"`
	HitsSemantic   int     `json:"hits_semantic"`
	HitRate        float64 `json:"hit_rate"`
	WrongHits      int     `json:"wrong_hits"`
	WrongHitRate   float64 `json:"wrong_hit_rate"`
	P50Ms          float64 `json:"p50_ms"`
	P99Ms          float64 `json:"p99_ms"`
}

func pct(ds []time.Duration, p float64) float64 {
	if len(ds) == 0 {
		return 0
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	idx := int(p * float64(len(ds)-1))
	return float64(ds[idx].Microseconds()) / 1000
}

// Summarize computes hit and wrong-hit rates. groups maps every answered request id to its group.
func Summarize(results []Result, groups map[string]string) Report {
	var r Report
	var lat []time.Duration
	for _, x := range results {
		r.Requests++
		if x.Status != 200 {
			r.Errors++
		} else {
			lat = append(lat, x.Latency)
		}
		if x.Phase != 2 {
			continue
		}
		r.Phase2Requests++
		switch x.Cache {
		case "hit-exact":
			r.HitsExact++
		case "hit-semantic":
			r.HitsSemantic++
		default:
			continue
		}
		if groups[x.Source] != x.Group || x.Group[0] == 'n' {
			r.WrongHits++
		}
	}
	if hits := r.HitsExact + r.HitsSemantic; hits > 0 {
		r.WrongHitRate = float64(r.WrongHits) / float64(hits)
	}
	if r.Phase2Requests > 0 {
		r.HitRate = float64(r.HitsExact+r.HitsSemantic) / float64(r.Phase2Requests)
	}
	r.P50Ms, r.P99Ms = pct(lat, 0.5), pct(lat, 0.99)
	return r
}
