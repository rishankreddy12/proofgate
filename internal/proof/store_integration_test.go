//go:build integration

package proof

import (
	"context"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/analytics"
	"github.com/stretchr/testify/require"
	tcch "github.com/testcontainers/testcontainers-go/modules/clickhouse"
)

func chConn(t *testing.T) string {
	ctx := context.Background()
	c, err := tcch.Run(ctx, "clickhouse/clickhouse-server:24.8-alpine",
		tcch.WithUsername("pg"), tcch.WithPassword("pg"), tcch.WithDatabase("proofgate"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, err := c.ConnectionString(ctx)
	require.NoError(t, err)
	return dsn
}

func TestProofStoreIntegration(t *testing.T) {
	ctx := context.Background()
	conn, err := analytics.Open(ctx, chConn(t))
	require.NoError(t, err)
	require.NoError(t, analytics.Migrate(ctx, conn))

	ch := NewCH(conn)
	now := time.Now().UTC().Truncate(time.Millisecond)

	// 1. Insert Shadow records
	sr1 := ShadowRecord{
		ID:              "s1",
		TS:              now.Add(-2 * time.Minute),
		TenantID:        "t1",
		Route:           "chat",
		Threshold:       0.85,
		Similarity:      0.88,
		Query:           "hello world",
		CandidateQuery:  "hello there",
		CandidateAnswer: "hi",
		ActualAnswer:    "hello!",
		CandidateSource: "exact",
	}
	sr2 := ShadowRecord{
		ID:              "s2",
		TS:              now.Add(-1 * time.Minute),
		TenantID:        "t1",
		Route:           "chat",
		Threshold:       0.85,
		Similarity:      0.92,
		Query:           "foo bar",
		CandidateQuery:  "foo bar baz",
		CandidateAnswer: "baz",
		ActualAnswer:    "baz!",
		CandidateSource: "approx",
	}
	require.NoError(t, ch.InsertShadow(ctx, []ShadowRecord{sr1, sr2}))

	// 2. UnlabeledShadow: both should be unlabeled
	unlabeled, err := ch.UnlabeledShadow(ctx, "chat", 0.80, 0.95, 10)
	require.NoError(t, err)
	require.Len(t, unlabeled, 2)
	require.Equal(t, "s2", unlabeled[0].ID) // ordered by ts DESC
	require.Equal(t, "s1", unlabeled[1].ID)

	// 3. Insert judge label for s1
	l1 := CacheLabel{
		ShadowID:           "s1",
		TS:                 now.Add(-1 * time.Minute),
		Rater:              "judge",
		RaterID:            "gpt-4o",
		Acceptable:         true,
		Reason:             "equivalent answers",
		JudgePromptVersion: "v1",
	}
	require.NoError(t, ch.InsertLabels(ctx, []CacheLabel{l1}))

	// UnlabeledShadow should now only return s2
	unlabeledAfter, err := ch.UnlabeledShadow(ctx, "chat", 0.80, 0.95, 10)
	require.NoError(t, err)
	require.Len(t, unlabeledAfter, 1)
	require.Equal(t, "s2", unlabeledAfter[0].ID)

	// 4. Insert human label for s1 (human overrides judge)
	l1Human := CacheLabel{
		ShadowID:           "s1",
		TS:                 now,
		Rater:              "human",
		RaterID:            "admin",
		Acceptable:         false,
		Reason:             "subtle difference",
		JudgePromptVersion: "",
	}
	require.NoError(t, ch.InsertLabels(ctx, []CacheLabel{l1Human}))

	// 5. CurveInputs: verify similarities and labeled point
	sims, pts, err := ch.CurveInputs(ctx, "chat", now.Add(-5*time.Minute))
	require.NoError(t, err)
	require.Len(t, sims, 2)
	require.Len(t, pts, 1)
	require.Equal(t, "human", pts[0].Rater)
	require.False(t, pts[0].Acceptable) // Human override is false

	// 6. HumanAndJudge: check paired labels
	judgeLabels, humanLabels, err := ch.HumanAndJudge(ctx, "chat")
	require.NoError(t, err)
	require.Len(t, judgeLabels, 1)
	require.Len(t, humanLabels, 1)
	require.True(t, judgeLabels[0])
	require.False(t, humanLabels[0])

	// 7. Routing Shadow & Deltas
	rs1 := RoutingShadow{
		ID:                 "r1",
		TS:                 now.Add(-30 * time.Second),
		TenantID:           "t1",
		Route:              "chat",
		Decision:           "cheap",
		CheapTarget:        "mock-a",
		StrongTarget:       "mock-b",
		Prompt:             "tell me a joke",
		CheapAnswer:        "a joke",
		StrongAnswer:       "a funny joke",
		CheapScore:         0.9,
		StrongScore:        0.95,
		JudgeModel:         "gpt-4o",
		JudgePromptVersion: "v1",
	}
	require.NoError(t, ch.InsertRoutingShadow(ctx, []RoutingShadow{rs1}))

	deltas, err := ch.RoutingDeltas(ctx, "chat", now.Add(-5*time.Minute))
	require.NoError(t, err)
	require.Len(t, deltas, 1)
	require.InDelta(t, -0.05, deltas[0], 0.001)

	// 8. Routing Decisions & Savings
	rd1 := RoutingDecision{
		TS:                   now.Add(-20 * time.Second),
		RequestID:            "req-1",
		TenantID:             "t1",
		Route:                "chat",
		Decision:             "cheap",
		Reason:               "high_confidence",
		Score:                0.9,
		CostMicros:           50,
		CounterfactualMicros: 200,
	}
	rd2 := RoutingDecision{
		TS:                   now.Add(-10 * time.Second),
		RequestID:            "req-2",
		TenantID:             "t1",
		Route:                "chat",
		Decision:             "strong",
		Reason:               "low_confidence",
		Score:                0.4,
		CostMicros:           200,
		CounterfactualMicros: 200,
	}
	require.NoError(t, ch.InsertDecisions(ctx, []RoutingDecision{rd1, rd2}))

	actual, counterfactual, cheapShare, n, err := ch.RoutingSavings(ctx, "chat", now.Add(-5*time.Minute))
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.EqualValues(t, 250, actual)
	require.EqualValues(t, 400, counterfactual)
	require.InDelta(t, 0.5, cheapShare, 0.001)
}
