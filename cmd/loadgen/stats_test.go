package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestPcts(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 100; i++ {
		ds = append(ds, ms(i))
	}
	p := Pcts(ds)
	require.InDelta(t, 50, p.P50, 0.001)
	require.InDelta(t, 90, p.P90, 0.001)
	require.InDelta(t, 99, p.P99, 0.001)
	require.InDelta(t, 100, p.Max, 0.001)
	require.InDelta(t, 50.5, p.Mean, 0.001)
	require.Equal(t, Percentiles{}, Pcts(nil))
}

func TestSummarizeSkipsErrorsAndCountsRate(t *testing.T) {
	recs := []Record{
		{TTFT: ms(10), Total: ms(100), Status: 200},
		{TTFT: ms(20), Total: ms(200), Status: 200},
		{Status: 500},
	}
	s := Summarize(recs, "l", "u", "r", 10, 2*time.Second, map[string]any{"commit": "abc"})
	require.Equal(t, 3, s.Requests)
	require.Equal(t, 1, s.Errors)
	require.InDelta(t, 15, s.TTFT.Mean, 0.001)
	require.InDelta(t, 1.0, s.Achieved, 0.001) // 2 successful requests in 2 s
	require.Equal(t, "abc", s.Meta["commit"])
}
