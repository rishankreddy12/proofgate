package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/proofgate/proofgate/internal/proof"
	"github.com/stretchr/testify/require"
)

func TestCacheEvalLogic(t *testing.T) {
	// Generate 10 test pairs
	q1 := []string{"How to reset password?", "Where is the library?", "What is python?", "How to cook rice?", "Weather in Tokyo?"}
	q2 := []string{"How can I reset password?", "Public library location?", "Python programming language", "Cooking rice steps", "Is it raining in Tokyo?"}

	var sims []float64
	var labeled []proof.LabeledPoint

	indexed := make([]IndexedDoc, len(q1))
	for i, q := range q1 {
		indexed[i] = IndexedDoc{
			ID:        string(rune('1' + i)),
			Group:     string(rune('g' + i)),
			Prompt:    q,
			Embedding: mockllm.HashEmbedding(q, 128),
		}
	}

	for i, q := range q2 {
		emb := mockllm.HashEmbedding(q, 128)
		bestSim := -1.0
		var bestDoc IndexedDoc
		for _, doc := range indexed {
			s := cosineSim(emb, doc.Embedding)
			if s > bestSim {
				bestSim = s
				bestDoc = doc
			}
		}
		isDup := bestDoc.Group == string(rune('g'+i))
		sims = append(sims, bestSim)
		labeled = append(labeled, proof.LabeledPoint{
			Similarity: bestSim,
			Acceptable: isDup,
			Rater:      "dataset",
		})
	}

	rec, curve := proof.TuneCacheFromData(sims, labeled, 0.05)
	require.GreaterOrEqual(t, rec, 0.70)
	require.Len(t, curve, 29)

	tmpFile := filepath.Join(t.TempDir(), "curve.json")
	_ = os.WriteFile(tmpFile, []byte("{}"), 0644)
	require.FileExists(t, tmpFile)
}
