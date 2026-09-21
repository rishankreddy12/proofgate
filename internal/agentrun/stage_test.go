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

type fakeStore struct {
	next    StepResult
	charged []int64
}

func (f *fakeStore) Step(context.Context, string, string, store.RunPolicy, string) (StepResult, error) {
	return f.next, nil
}
func (f *fakeStore) Charge(_ context.Context, _, _ string, _ store.RunPolicy, micros int64, _ int) error {
	f.charged = append(f.charged, micros)
	return nil
}

func runCall(p *store.RunPolicy, runID string) *pipeline.Call {
	c := pipeline.NewCall(auth.Principal{TenantID: "t", Key: store.KeyPolicy{Run: p}}, &api.ChatRequest{
		Messages: []api.Message{{Role: "user", Content: api.Content{Text: "x"}}}}, &router.Route{Name: "r"})
	c.Incoming = http.Header{}
	if runID != "" {
		c.Incoming.Set("X-ProofGate-Run-Id", runID)
	}
	return c
}

func TestRunStage(t *testing.T) {
	fs := &fakeStore{next: StepResult{Status: Allowed, Steps: 2, CostMicros: 300}}
	s := NewStage(fs)
	p := &store.RunPolicy{MaxCostUSD: 0.001}

	c := runCall(p, "run-7")
	_, err := s.Before(context.Background(), c)
	require.NoError(t, err)
	require.Equal(t, "2", c.Header.Get("X-ProofGate-Run-Steps"))
	require.Equal(t, "0.000700", c.Header.Get("X-ProofGate-Run-Remaining-USD"))
	require.Equal(t, "run-7", c.Values["run.id"])
	c.CostMicros = 50
	s.After(context.Background(), c)
	require.Equal(t, []int64{50}, fs.charged)

	var ae *api.Error
	fs.next = StepResult{Status: Loop, Repeats: 3}
	_, err = s.Before(context.Background(), runCall(p, "run-7"))
	require.ErrorAs(t, err, &ae)
	require.Equal(t, 429, ae.Status)
	require.Equal(t, "agent_loop_detected", ae.Code)

	fs.next = StepResult{Status: CostExceeded}
	_, err = s.Before(context.Background(), runCall(p, "run-7"))
	require.ErrorAs(t, err, &ae)
	require.Equal(t, 402, ae.Status)
	require.Equal(t, "run_budget_exceeded", ae.Code)

	_, err = s.Before(context.Background(), runCall(&store.RunPolicy{RequireRunID: true}, ""))
	require.ErrorAs(t, err, &ae)
	require.Equal(t, 400, ae.Status)
	_, err = s.Before(context.Background(), runCall(p, "bad id with spaces"))
	require.ErrorAs(t, err, &ae)

	c = runCall(nil, "run-7")
	_, err = s.Before(context.Background(), c)
	require.NoError(t, err)
	require.Nil(t, c.Values["run.id"], "keys without a run policy are not tracked")
}
