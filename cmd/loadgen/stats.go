package main

import (
	"sort"
	"time"
)

type Record struct {
	Start  time.Duration
	TTFT   time.Duration
	Total  time.Duration
	Status int
	Target string
}

type Percentiles struct {
	P50  float64 `json:"p50_ms"`
	P90  float64 `json:"p90_ms"`
	P99  float64 `json:"p99_ms"`
	Max  float64 `json:"max_ms"`
	Mean float64 `json:"mean_ms"`
}

type Summary struct {
	Label    string         `json:"label"`
	URL      string         `json:"url"`
	Route    string         `json:"route"`
	RPS      float64        `json:"requested_rps"`
	Duration float64        `json:"duration_s"`
	Requests int            `json:"requests"`
	Errors   int            `json:"errors"`
	Overload int            `json:"overload"`
	Achieved float64        `json:"achieved_rps"`
	TTFT     Percentiles    `json:"ttft"`
	Total    Percentiles    `json:"total"`
	ByTarget map[string]int `json:"by_target"`
	Meta     map[string]any `json:"meta"`
}

func Pcts(ds []time.Duration) Percentiles {
	if len(ds) == 0 {
		return Percentiles{}
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(q float64) float64 {
		i := int(q * float64(len(sorted)-1)) // floor: p50 of 1..100ms is 50ms
		return float64(sorted[i].Microseconds()) / 1000
	}
	var sum time.Duration
	for _, d := range sorted {
		sum += d
	}
	return Percentiles{
		P50:  at(0.5),
		P90:  at(0.9),
		P99:  at(0.99),
		Max:  float64(sorted[len(sorted)-1].Microseconds()) / 1000,
		Mean: float64(sum.Microseconds()) / 1000 / float64(len(sorted)),
	}
}

func Summarize(recs []Record, label, url, route string, rps float64, dur time.Duration, meta map[string]any) Summary {
	s := Summary{
		Label:    label,
		URL:      url,
		Route:    route,
		RPS:      rps,
		Duration: dur.Seconds(),
		Requests: len(recs),
		ByTarget: map[string]int{},
		Meta:     meta,
	}
	var ttft, total []time.Duration
	for _, r := range recs {
		switch {
		case r.Status == 0:
			s.Overload++
			s.Errors++
		case r.Status != 200:
			s.Errors++
		default:
			if r.TTFT > 0 {
				ttft = append(ttft, r.TTFT)
			}
			total = append(total, r.Total)
			s.ByTarget[r.Target]++
		}
	}
	s.TTFT, s.Total = Pcts(ttft), Pcts(total)
	if dur > 0 {
		s.Achieved = float64(len(total)) / dur.Seconds()
	}
	return s
}
