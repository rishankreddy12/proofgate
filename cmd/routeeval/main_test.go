package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/smartroute"
	"github.com/proofgate/proofgate/internal/stats"
	"github.com/stretchr/testify/require"
)

func TestRouteEvalBenchmarkLogic(t *testing.T) {
	classifier := smartroute.NewRuleClassifier()
	cheapCount := 0

	for _, item := range defaultBenchmark {
		req := &api.ChatRequest{
			Messages: []api.Message{
				{Role: "user", Content: api.Content{Text: item.Query}},
			},
		}
		res := classifier.Classify(req, 1500)
		if res.Decision == smartroute.DecisionCheap {
			cheapCount++
		}
	}

	require.Greater(t, cheapCount, 0)
	require.Less(t, cheapCount, len(defaultBenchmark))

	// Verify bootstrap calculation
	deltas := []float64{-0.01, 0.0, 0.01, 0.0, -0.005}
	mean, lo, hi := stats.BootstrapMeanCI(deltas, 100, 0.05, 42)
	require.InDelta(t, -0.001, mean, 0.01)
	require.Less(t, lo, hi)

	tmp := filepath.Join(t.TempDir(), "test_route_eval.json")
	_ = os.WriteFile(tmp, []byte("{}"), 0644)
	require.FileExists(t, tmp)
}
