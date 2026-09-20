package smartroute

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type staticKNNIndex struct {
	neighbors []Neighbor
}

func (s *staticKNNIndex) Nearest(_ context.Context, _ []float32, _ int) ([]Neighbor, error) {
	return s.neighbors, nil
}

func TestKNNClassifier_MajorityVote(t *testing.T) {
	ctx := context.Background()

	// 7 neighbors, 5 cheap-safe (CheapScore >= StrongScore - 0.05), 2 not
	var neighbors []Neighbor
	for i := 0; i < 5; i++ {
		neighbors = append(neighbors, Neighbor{
			Example: LabeledExample{
				ID:          "cheap-ex",
				CheapScore:  0.90,
				StrongScore: 0.92, // 0.90 >= 0.92 - 0.05 -> cheap-safe
			},
			Distance: 0.1,
		})
	}
	for i := 0; i < 2; i++ {
		neighbors = append(neighbors, Neighbor{
			Example: LabeledExample{
				ID:          "strong-ex",
				CheapScore:  0.60,
				StrongScore: 0.95, // 0.60 < 0.95 - 0.05 -> not cheap-safe
			},
			Distance: 0.1,
		})
	}

	index := &staticKNNIndex{neighbors: neighbors}
	classifier := NewKNNClassifier(nil, index, 7, 0.70)

	class, confidence, err := classifier.Classify(ctx, "sample query")
	require.NoError(t, err)
	require.Equal(t, DecisionCheap, class.Decision)
	require.Equal(t, ReasonKNN, class.Reason)
	// 5 out of 7 with equal distance gives 5/7 ≈ 0.714 > 0.70
	require.Greater(t, confidence, 0.70)
}

func TestKNNClassifier_AmbiguousVote(t *testing.T) {
	ctx := context.Background()

	// 7 neighbors, 4 cheap-safe, 3 not -> 4/7 ≈ 0.57 < 0.70 threshold
	var neighbors []Neighbor
	for i := 0; i < 4; i++ {
		neighbors = append(neighbors, Neighbor{
			Example: LabeledExample{
				ID:          "cheap-ex",
				CheapScore:  0.90,
				StrongScore: 0.90,
			},
			Distance: 0.1,
		})
	}
	for i := 0; i < 3; i++ {
		neighbors = append(neighbors, Neighbor{
			Example: LabeledExample{
				ID:          "strong-ex",
				CheapScore:  0.50,
				StrongScore: 0.95,
			},
			Distance: 0.1,
		})
	}

	index := &staticKNNIndex{neighbors: neighbors}
	classifier := NewKNNClassifier(nil, index, 7, 0.70)

	class, confidence, err := classifier.Classify(ctx, "sample query")
	require.NoError(t, err)
	require.Equal(t, DecisionStrong, class.Decision, "ambiguous vote below threshold must fall back to strong")
	require.InDelta(t, 1.0-4.0/7.0, confidence, 0.01)
}
