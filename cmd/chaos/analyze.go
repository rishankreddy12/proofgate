package main

import (
	"sort"
	"time"
)

type Obs struct {
	At     time.Duration
	Target string
	TTFT   time.Duration
	Status int
}

func FailoverTime(obs []Obs, switchAt time.Duration, healthy string, share float64, bucket time.Duration) (time.Duration, bool) {
	type agg struct{ ok, good int }
	buckets := map[int]*agg{}
	last := 0
	for _, o := range obs {
		if o.At < switchAt || o.Status != 200 {
			continue
		}
		i := int((o.At - switchAt) / bucket)
		if buckets[i] == nil {
			buckets[i] = &agg{}
		}
		buckets[i].ok++
		if o.Target == healthy {
			buckets[i].good++
		}
		last = max(last, i)
	}
	first := -1
	for i := last; i >= 0; i-- { // walk back from the end while the share holds
		b := buckets[i]
		if b == nil || b.ok == 0 || float64(b.good)/float64(b.ok) < share {
			break
		}
		first = i
	}
	if first < 0 {
		return 0, false
	}
	return time.Duration(first) * bucket, true
}

func p95(ds []time.Duration) float64 {
	if len(ds) == 0 {
		return 0
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return float64(ds[int(0.95*float64(len(ds)-1))].Microseconds()) / 1000
}
