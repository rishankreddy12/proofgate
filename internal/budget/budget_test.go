package budget

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

type memLedger struct {
	spent map[string]int64
	err   error
}

func (m *memLedger) Spent(_ context.Context, t, month string) (int64, error) { return m.spent[t+month], m.err }
func (m *memLedger) Add(_ context.Context, t, month string, v int64) error {
	m.spent[t+month] += v
	return m.err
}

var fixed = func() time.Time { return time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC) }

func newCall(budgetUSD float64, strict bool) *pipeline.Call {
	return pipeline.NewCall(auth.Principal{TenantID: "t", Tenant: store.TenantPolicy{MonthlyBudgetUSD: budgetUSD, Strict: strict}},
		&api.ChatRequest{}, nil)
}

func TestBudgetStage(t *testing.T) {
	l := &memLedger{spent: map[string]int64{}}
	s := NewStage(l, fixed)
	c := newCall(1, false)
	_, err := s.Before(context.Background(), c)
	require.NoError(t, err)
	require.Equal(t, "1.000000", c.Header.Get("X-ProofGate-Budget-Remaining-USD"))
	c.CostMicros = 1_000_000
	s.After(context.Background(), c)
	require.EqualValues(t, 1_000_000, l.spent["t2026-09"])

	_, err = s.Before(context.Background(), newCall(1, false))
	var ae *api.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, 402, ae.Status)
	require.Equal(t, "budget_exceeded", ae.Code)
}

func TestBudgetUnlimitedAndLedgerDown(t *testing.T) {
	l := &memLedger{spent: map[string]int64{}, err: errors.New("down")}
	s := NewStage(l, fixed)
	_, err := s.Before(context.Background(), newCall(0, false))
	require.NoError(t, err, "no budget configured")
	_, err = s.Before(context.Background(), newCall(5, false))
	require.NoError(t, err, "fail open")
	_, err = s.Before(context.Background(), newCall(5, true))
	var ae *api.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, 503, ae.Status)
}
