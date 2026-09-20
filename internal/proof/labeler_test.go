package proof

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mockLabelStore struct {
	unlabeled map[string][]ShadowRecord // key: route+lo+hi
	inserted  []CacheLabel
}

func (m *mockLabelStore) UnlabeledShadow(_ context.Context, route string, lo, hi float64, limit int) ([]ShadowRecord, error) {
	var out []ShadowRecord
	for _, rec := range m.unlabeled[route] {
		if rec.Similarity >= lo && rec.Similarity < hi {
			out = append(out, rec)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *mockLabelStore) InsertLabels(_ context.Context, ls []CacheLabel) error {
	m.inserted = append(m.inserted, ls...)
	return nil
}

type mockJudge struct {
	evals int
}

func (m *mockJudge) EvaluateCache(_ context.Context, _ ShadowRecord) (CacheEvalResult, error) {
	m.evals++
	return CacheEvalResult{
		Acceptable:         true,
		Reason:             "equivalent response",
		JudgePromptVersion: JudgeCachePromptVersion,
	}, nil
}

type staticLeader struct{ leader bool }

func (s staticLeader) IsLeader() bool { return s.leader }

type mockSpendGuard struct {
	canSpend bool
	spent    int64
}

func (m *mockSpendGuard) CanSpend(_ context.Context, _ int64) (bool, error) {
	return m.canSpend, nil
}

func (m *mockSpendGuard) AddSpend(_ context.Context, micros int64) error {
	m.spent += micros
	return nil
}

func (m *mockSpendGuard) SpentToday(_ context.Context) (int64, error) {
	return m.spent, nil
}

func (m *mockSpendGuard) Limit() int64 {
	return 5_000_000
}

func TestCacheLabelWorker_StratifiedBatch(t *testing.T) {
	ctx := context.Background()

	store := &mockLabelStore{
		unlabeled: map[string][]ShadowRecord{
			"faq": {
				// 2 records in [0.70, 0.80)
				{ID: "s1", Similarity: 0.72, Query: "q1"},
				{ID: "s2", Similarity: 0.78, Query: "q2"},
				// 3 records in [0.80, 0.90)
				{ID: "s3", Similarity: 0.82, Query: "q3"},
				{ID: "s4", Similarity: 0.85, Query: "q4"},
				{ID: "s5", Similarity: 0.89, Query: "q5"},
				// 2 records in [0.90, 0.98)
				{ID: "s6", Similarity: 0.92, Query: "q6"},
				{ID: "s7", Similarity: 0.95, Query: "q7"},
			},
		},
	}

	judge := &mockJudge{}
	spend := &mockSpendGuard{canSpend: true}
	leader := &staticLeader{leader: true}

	worker := NewCacheLabelWorker(leader, spend, store, judge, []string{"faq"}, "gpt-4o", time.Minute)
	n, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 7, n)
	require.Equal(t, 7, judge.evals)
	require.Len(t, store.inserted, 7)
	require.Equal(t, int64(7000), spend.spent)

	// Verify label fields
	for _, l := range store.inserted {
		require.Equal(t, "judge", l.Rater)
		require.Equal(t, "gpt-4o", l.RaterID)
		require.True(t, l.Acceptable)
		require.Equal(t, "equivalent response", l.Reason)
		require.Equal(t, JudgeCachePromptVersion, l.JudgePromptVersion)
	}

	// If not leader, does nothing
	leader.leader = false
	n2, err2 := worker.RunOnce(ctx)
	require.NoError(t, err2)
	require.Equal(t, 0, n2)

	// If budget depleted, stops
	leader.leader = true
	spend.canSpend = false
	n3, err3 := worker.RunOnce(ctx)
	require.NoError(t, err3)
	require.Equal(t, 0, n3)
}
