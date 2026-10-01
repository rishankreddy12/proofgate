package budget

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

type memLedger struct {
	mu    sync.Mutex
	spent map[string]int64
	err   error
}

func (m *memLedger) Spent(_ context.Context, t, month string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spent[t+month], m.err
}

func (m *memLedger) Add(_ context.Context, t, month string, v int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	next := m.spent[t+month] + v
	if next < 0 {
		next = 0
	}
	m.spent[t+month] = next
	return nil
}

func (m *memLedger) Reserve(_ context.Context, t, month string, est, limit int64) (bool, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return false, 0, m.err
	}
	cur := m.spent[t+month]
	if cur+est > limit {
		return false, cur, nil
	}
	m.spent[t+month] = cur + est
	return true, cur + est, nil
}

var fixed = func() time.Time { return time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC) }

func newCall(budgetUSD float64, strict bool, targetStr ...string) *pipeline.Call {
	var rt *router.Route
	var tgt router.Target
	if len(targetStr) > 0 && targetStr[0] != "" {
		if parsed, ok := router.ParseTarget(targetStr[0]); ok {
			tgt = parsed
			rt = &router.Route{Name: "default", Targets: []router.Target{parsed}}
		}
	}
	c := pipeline.NewCall(
		auth.Principal{
			TenantID: "t",
			Tenant:   store.TenantPolicy{MonthlyBudgetUSD: budgetUSD, Strict: strict},
		},
		&api.ChatRequest{
			Model: "default",
			Messages: []api.Message{
				{Role: "user", Content: api.Content{Text: "hello world"}},
			},
		},
		rt,
	)
	c.Target = tgt
	return c
}

func TestBudgetStage_ReservationAndReconcile(t *testing.T) {
	l := &memLedger{spent: map[string]int64{}}
	pricing := NewPricing(map[string]config.Price{
		"mock/gpt-4o": {Input: 1, Output: 2},
	})
	s := NewStage(l, fixed, StageOpts{
		Pricing:       pricing,
		ReserveStrict: true,
		DefaultMax:    100,
	})

	// Prompt ~ 2 tokens, max 100 tokens => est = 2*1 + 100*2 = 202 micros ($0.000202)
	// Budget: $1.00 = 1,000,000 micros
	c1 := newCall(1.0, false, "mock/gpt-4o")
	_, err := s.Before(context.Background(), c1)
	require.NoError(t, err)

	reserved, ok := c1.Values["budget.reserved"].(int64)
	require.True(t, ok)
	require.Greater(t, reserved, int64(0))
	require.Equal(t, "0.999791", c1.Header.Get("X-ProofGate-Budget-Remaining-USD"))

	// Actual cost was only 50 micros: After reconciles delta = 50 - reserved
	c1.CostMicros = 50
	s.After(context.Background(), c1)

	spent, _ := l.Spent(context.Background(), "t", "2026-09")
	require.EqualValues(t, 50, spent, "reconcile must refund unused reservation")

	// Call 2: Full refund on cache hit or error (CostMicros == 0)
	c2 := newCall(1.0, false, "mock/gpt-4o")
	_, err = s.Before(context.Background(), c2)
	require.NoError(t, err)

	// Simulate cache hit: CostMicros = 0
	c2.CostMicros = 0
	s.After(context.Background(), c2)

	spentAfterRefund, _ := l.Spent(context.Background(), "t", "2026-09")
	require.EqualValues(t, 50, spentAfterRefund, "CostMicros==0 must fully refund the reserved amount")
}

func TestBudgetStage_ConcurrentReservationLimit(t *testing.T) {
	l := &memLedger{spent: map[string]int64{}}
	pricing := NewPricing(map[string]config.Price{
		"mock/gpt-4o": {Input: 1, Output: 1},
	})
	promptTokens := newCall(0, false, "mock/gpt-4o").Request.EstimatePromptTokens()
	s := NewStage(l, fixed, StageOpts{
		Pricing:       pricing,
		ReserveStrict: true,
		DefaultMax:    100 - promptTokens, // prompt + max = exactly 100 micros per call
	})

	// Budget of $0.001 = 1,000 micros => fits exactly 10 calls of 100 micros
	budgetUSD := 0.001
	totalRequests := 100

	var wg sync.WaitGroup
	var admitted int32
	var rejected int32

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := newCall(budgetUSD, false, "mock/gpt-4o")
			_, err := s.Before(context.Background(), c)
			if err == nil {
				atomic.AddInt32(&admitted, 1)
				// Do not call After to keep reservation active
			} else {
				var ae *api.Error
				if errors.As(err, &ae) && ae.Status == 402 && ae.Code == "budget_exceeded" {
					atomic.AddInt32(&rejected, 1)
				}
			}
		}()
	}
	wg.Wait()

	require.EqualValues(t, 10, admitted, "atomic reservation must admit exactly 10 requests")
	require.EqualValues(t, 90, rejected, "atomic reservation must reject exactly 90 requests")
}

func TestBudgetStage_RequirePricing(t *testing.T) {
	l := &memLedger{spent: map[string]int64{}}
	pricing := NewPricing(map[string]config.Price{
		"mock/gpt-4o": {Input: 1, Output: 2},
	})
	s := NewStage(l, fixed, StageOpts{
		Pricing:        pricing,
		ReserveStrict:  true,
		RequirePricing: true,
		DefaultMax:     100,
	})

	// Unpriced target "mock/unpriced-model"
	c := newCall(1.0, false, "mock/unpriced-model")
	_, err := s.Before(context.Background(), c)
	require.Error(t, err)

	var ae *api.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, 500, ae.Status)
	require.Equal(t, "unpriced_target", ae.Code)
}

func TestBudgetStage_ReserveOffFallback(t *testing.T) {
	l := &memLedger{spent: map[string]int64{"t2026-09": 1_000_000}}
	s := NewStage(l, fixed, StageOpts{
		ReserveStrict: false, // budget.reserve: off
	})

	c := newCall(1.0, false)
	_, err := s.Before(context.Background(), c)
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
