package proof

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mockRoutingShadowStore struct {
	inserted []RoutingShadow
}

func (m *mockRoutingShadowStore) InsertRoutingShadow(_ context.Context, rs []RoutingShadow) error {
	m.inserted = append(m.inserted, rs...)
	return nil
}

type mockRoutingJudge struct {
	cheapScore  float64
	strongScore float64
}

func (m *mockRoutingJudge) EvaluateRouting(_ context.Context, _, _, _ string) (RoutingEvalResult, error) {
	return RoutingEvalResult{
		ScoreCheap:         m.cheapScore,
		ScoreStrong:        m.strongScore,
		Reason:             "both correct",
		JudgePromptVersion: JudgeRoutingPromptVersion,
	}, nil
}

func TestRoutingShadow_PairEvaluation(t *testing.T) {
	ctx := context.Background()
	store := &mockRoutingShadowStore{}
	judge := &mockRoutingJudge{cheapScore: 0.85, strongScore: 0.95}
	leader := &staticLeader{leader: true}
	spend := &mockSpendGuard{canSpend: true}

	worker := NewRoutingShadowWorker(store, judge, leader, spend)
	worker.SetNow(func() time.Time { return time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC) })

	task := RoutingShadowTask{
		TenantID:     "t1",
		Route:        "chat",
		Decision:     "cheap",
		CheapTarget:  "mock-a/small",
		StrongTarget: "mock-b/large",
		Prompt:       "Solve 2+2",
		CheapAnswer:  "4",
		StrongAnswer: "2+2 = 4",
		JudgeModel:   "gpt-4o",
	}

	rs, err := worker.EvaluateAndRecord(ctx, task)
	require.NoError(t, err)
	require.NotNil(t, rs)
	require.NotEmpty(t, rs.ID)
	require.Equal(t, 0.85, rs.CheapScore)
	require.Equal(t, 0.95, rs.StrongScore)
	require.Equal(t, "mock-a/small", rs.CheapTarget)
	require.Equal(t, "mock-b/large", rs.StrongTarget)

	require.Len(t, store.inserted, 1)
	require.Equal(t, rs.ID, store.inserted[0].ID)
	require.Equal(t, JudgeRoutingPromptVersion, store.inserted[0].JudgePromptVersion)
	require.Equal(t, int64(1000), spend.spent)
}
