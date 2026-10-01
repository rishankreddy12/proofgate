package agentrun

import (
	"context"
	"net/http"
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

type mockEmbedder struct {
	fn func(text string) []float32
}

func (m *mockEmbedder) Embed(_ context.Context, _, _, text string) ([]float32, error) {
	return m.fn(text), nil
}

func TestCosineSimilarity(t *testing.T) {
	v1 := []float32{1.0, 0.0, 0.0}
	v2 := []float32{1.0, 0.0, 0.0}
	v3 := []float32{0.0, 1.0, 0.0}
	v4 := []float32{-1.0, 0.0, 0.0}

	require.InDelta(t, 1.0, CosineSimilarity(v1, v2), 1e-6)
	require.InDelta(t, 0.0, CosineSimilarity(v1, v3), 1e-6)
	require.InDelta(t, -1.0, CosineSimilarity(v1, v4), 1e-6)
}

func TestEmbeddingSerialization(t *testing.T) {
	orig := []float32{0.1234, -0.5678, 1.0, 0.0, 999.99}
	encoded := EncodeEmbedding(orig)
	decoded := DecodeEmbedding(encoded)
	require.Equal(t, orig, decoded)
}

func TestFuzzyLoopDetector(t *testing.T) {
	// Mock embedder: texts mentioning "query_database" yield a vector very close to [1.0, 0.0]
	// Different texts yield orthogonal vectors [0.0, 1.0]
	emb := &mockEmbedder{
		fn: func(text string) []float32 {
			if text == "query_db_step" {
				return []float32{0.999, 0.01}
			} else if text == "query_db_step_variation" {
				return []float32{0.995, 0.02}
			}
			return []float32{0.0, 1.0}
		},
	}

	memStore := NewMemFuzzyStore()
	detector := NewFuzzyDetector(emb, memStore)

	p := store.RunPolicy{
		FuzzyLoop:      true,
		FuzzyThreshold: 0.95,
		LoopRepeats:    2,
		LoopWindow:     10,
	}.WithDefaults()

	req1 := &api.ChatRequest{
		Messages: []api.Message{
			{Role: "user", Content: api.Content{Text: "query_db_step"}},
		},
	}
	req2 := &api.ChatRequest{
		Messages: []api.Message{
			{Role: "user", Content: api.Content{Text: "query_db_step_variation"}},
		},
	}

	ctx := context.Background()

	// First step: recorded, no loop
	maxSim, repeats, isLoop, err := detector.Check(ctx, "t1", "run-1", p, req1)
	require.NoError(t, err)
	require.False(t, isLoop)
	require.Equal(t, 1, repeats)

	// Second step: textually different, but semantically 0.999 similar -> loop triggered!
	maxSim, repeats, isLoop, err = detector.Check(ctx, "t1", "run-1", p, req2)
	require.NoError(t, err)
	require.True(t, isLoop, "semantically similar step should trigger loop")
	require.Equal(t, 2, repeats)
	require.Greater(t, maxSim, 0.95)
}

func TestStageWithFuzzyDetector(t *testing.T) {
	emb := &mockEmbedder{
		fn: func(text string) []float32 {
			// All calls produce identical vectors to simulate a semantic loop
			return []float32{1.0, 0.0}
		},
	}
	memStore := NewMemFuzzyStore()
	detector := NewFuzzyDetector(emb, memStore)

	fs := &fakeStore{next: StepResult{Status: Allowed, Steps: 1}}
	s := NewStage(fs, detector)

	p := &store.RunPolicy{
		FuzzyLoop:      true,
		FuzzyThreshold: 0.95,
		LoopRepeats:    2,
		LoopWindow:     10,
	}

	call := func(text string) *pipeline.Call {
		c := pipeline.NewCall(auth.Principal{TenantID: "t", Key: store.KeyPolicy{Run: p}}, &api.ChatRequest{
			Messages: []api.Message{{Role: "user", Content: api.Content{Text: text}}}}, &router.Route{Name: "r"})
		c.Incoming = http.Header{}
		c.Incoming.Set("X-ProofGate-Run-Id", "run-fuzzy-1")
		return c
	}

	// Step 1: passes
	_, err := s.Before(context.Background(), call("search products with price < 100"))
	require.NoError(t, err)

	// Step 2: exact match doesn't catch because prompt is slightly different, but fuzzy detector catches it
	_, err = s.Before(context.Background(), call("search products with price under $100"))
	require.Error(t, err)

	var ae *api.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, 429, ae.Status)
	require.Equal(t, "agent_loop_detected", ae.Code)
}
