package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSummarize(t *testing.T) {
	// request id -> group for every request the gateway answered
	groups := map[string]string{"r1": "p1", "r2": "p2", "r3": "p1", "r4": "n2", "r5": "p3"}
	results := []Result{
		{ReqID: "r1", Phase: 1, Group: "p1", Status: 200, Cache: "miss", Latency: 10 * time.Millisecond},
		{ReqID: "r2", Phase: 1, Group: "p2", Status: 200, Cache: "miss", Latency: 20 * time.Millisecond},
		{ReqID: "r3", Phase: 2, Group: "p1", Status: 200, Cache: "hit-semantic", Source: "r1", Latency: 2 * time.Millisecond}, // correct
		{ReqID: "r4", Phase: 2, Group: "n2", Status: 200, Cache: "hit-semantic", Source: "r2", Latency: 2 * time.Millisecond}, // wrong: not a duplicate
		{ReqID: "r5", Phase: 2, Group: "p3", Status: 200, Cache: "miss", Latency: 30 * time.Millisecond},
		{Phase: 2, Group: "p4", Status: 502},
	}
	r := Summarize(results, groups)
	require.Equal(t, 6, r.Requests)
	require.Equal(t, 1, r.Errors)
	require.Equal(t, 4, r.Phase2Requests)
	require.Equal(t, 2, r.HitsSemantic)
	require.InDelta(t, 0.5, r.HitRate, 1e-9)
	require.Equal(t, 1, r.WrongHits)
	require.InDelta(t, 0.5, r.WrongHitRate, 1e-9)
}
