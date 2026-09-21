package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

var results = map[string]any{
	"overhead/stream-1000-gateway-1.json": map[string]any{
		"ttft":     map[string]any{"p50_ms": 3.2, "p99_ms": 11.75},
		"requests": float64(30000),
	},
}

func TestLookupAndMarkers(t *testing.T) {
	v, err := Lookup(results, "overhead/stream-1000-gateway-1.json:ttft.p99_ms")
	require.NoError(t, err)
	require.Equal(t, "11.75", v)
	v, _ = Lookup(results, "overhead/stream-1000-gateway-1.json:requests")
	require.Equal(t, "30,000", v)
	_, err = Lookup(results, "missing.json:x")
	require.Error(t, err)

	out, errs := ResolveMarkers("p99 TTFT overhead was {{bench:overhead/stream-1000-gateway-1.json:ttft.p99_ms}} ms", results)
	require.Empty(t, errs)
	require.Equal(t, "p99 TTFT overhead was 11.75 ms", out)

	_, errs = ResolveMarkers("{{bench:nope.json:a}}", results)
	require.Len(t, errs, 1)
}

func TestMedian(t *testing.T) {
	files := []map[string]any{
		{"ttft": map[string]any{"p99_ms": 10.0}},
		{"ttft": map[string]any{"p99_ms": 30.0}},
		{"ttft": map[string]any{"p99_ms": 20.0}},
	}
	v, ok := Median(files, "ttft.p99_ms")
	require.True(t, ok)
	require.Equal(t, 20.0, v)
}
