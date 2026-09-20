package smartroute

import (
	"context"
	"math"
)

type LabeledExample struct {
	ID          string
	Vector      []float32
	CheapScore  float64
	StrongScore float64
}

type Neighbor struct {
	Example  LabeledExample
	Distance float64
}

type KNNIndex interface {
	Nearest(ctx context.Context, embedding []float32, k int) ([]Neighbor, error)
}

type QueryEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

type QueryEmbedderFunc func(ctx context.Context, text string) ([]float32, error)

func (f QueryEmbedderFunc) Embed(ctx context.Context, text string) ([]float32, error) {
	return f(ctx, text)
}

type KNNClassifierImpl struct {
	embedder            QueryEmbedder
	index               KNNIndex
	k                   int
	confidenceThreshold float64
	epsilon             float64
}

func NewKNNClassifier(embedder QueryEmbedder, index KNNIndex, k int, threshold float64) *KNNClassifierImpl {
	if k <= 0 {
		k = 7
	}
	if threshold <= 0 {
		threshold = 0.70
	}
	return &KNNClassifierImpl{
		embedder:            embedder,
		index:               index,
		k:                   k,
		confidenceThreshold: threshold,
		epsilon:             0.05,
	}
}

func (c *KNNClassifierImpl) Classify(ctx context.Context, text string) (Classification, float64, error) {
	if c.index == nil {
		return Classification{Decision: DecisionStrong, Reason: ReasonNone}, 0.5, nil
	}

	var vec []float32
	if c.embedder != nil {
		v, err := c.embedder.Embed(ctx, text)
		if err != nil {
			return Classification{Decision: DecisionStrong, Reason: ReasonNone}, 0.5, err
		}
		vec = v
	}

	neighbors, err := c.index.Nearest(ctx, vec, c.k)
	if err != nil {
		return Classification{Decision: DecisionStrong, Reason: ReasonNone}, 0.5, err
	}
	if len(neighbors) == 0 {
		return Classification{Decision: DecisionStrong, Reason: ReasonNone}, 0.5, nil
	}

	var sumWeight float64
	var cheapWeight float64

	for _, n := range neighbors {
		dist := math.Max(0, n.Distance)
		w := 1.0 / (1.0 + dist)
		sumWeight += w

		// Cheap-safe if cheap_score >= strong_score - epsilon
		isCheapSafe := n.Example.CheapScore >= (n.Example.StrongScore - c.epsilon)
		if isCheapSafe {
			cheapWeight += w
		}
	}

	if sumWeight == 0 {
		return Classification{Decision: DecisionStrong, Reason: ReasonNone}, 0.5, nil
	}

	score := cheapWeight / sumWeight
	if score >= c.confidenceThreshold {
		return Classification{Decision: DecisionCheap, Reason: ReasonKNN}, score, nil
	}

	return Classification{Decision: DecisionStrong, Reason: ReasonKNN}, 1.0 - score, nil
}
