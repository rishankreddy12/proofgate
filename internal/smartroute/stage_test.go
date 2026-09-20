package smartroute

import (
	"context"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/proof"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/stretchr/testify/require"
)

type memDecisionRecorder struct {
	decisions []proof.RoutingDecision
}

func (m *memDecisionRecorder) EmitDecision(d proof.RoutingDecision) {
	m.decisions = append(m.decisions, d)
}

func TestSmartRouteStage_TargetMutation(t *testing.T) {
	recorder := &memDecisionRecorder{}
	stg := NewStage(recorder, nil)
	ctx := context.Background()

	rt := &router.Route{
		Name: "chat",
		SmartRoute: config.SmartRouteConfig{
			Mode:         "on",
			CheapTarget:  "mock-a/small",
			StrongTarget: "mock-b/large",
		},
	}

	// 1. Simple query -> Cheap
	req1 := reqWithText("hello")
	call1 := pipeline.NewCall(auth.Principal{TenantID: "t1"}, req1, rt)
	handled, err := stg.Before(ctx, call1)
	require.NoError(t, err)
	require.False(t, handled)
	require.Equal(t, "mock-a", call1.Target.Provider)
	require.Equal(t, "small", call1.Target.Model)
	require.Equal(t, "cheap", call1.Values["routing.decision"])
	require.Equal(t, "rule_greeting", call1.Values["routing.reason"])

	// 2. Code query -> Strong
	req2 := reqWithText("def quicksort(arr):")
	call2 := pipeline.NewCall(auth.Principal{TenantID: "t1"}, req2, rt)
	handled, err = stg.Before(ctx, call2)
	require.NoError(t, err)
	require.False(t, handled)
	require.Equal(t, "mock-b", call2.Target.Provider)
	require.Equal(t, "large", call2.Target.Model)
	require.Equal(t, "strong", call2.Values["routing.decision"])
	require.Equal(t, "rule_code", call2.Values["routing.reason"])

	// After records decision
	call1.CostMicros = 50
	stg.SetNow(func() time.Time { return time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC) })
	stg.After(ctx, call1)

	require.Len(t, recorder.decisions, 1)
	d := recorder.decisions[0]
	require.Equal(t, "cheap", d.Decision)
	require.Equal(t, "rule_greeting", d.Reason)
	require.EqualValues(t, 50, d.CostMicros)
	require.EqualValues(t, 200, d.CounterfactualMicros) // 4x for cheap
}
