package server

import (
	"context"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/analytics"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/budget"
	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/stretchr/testify/require"
)

type memLedger struct {
	spent map[string]int64
}

func (m *memLedger) Spent(_ context.Context, tenantID, month string) (int64, error) {
	return m.spent[tenantID+":"+month], nil
}

func (m *memLedger) Add(_ context.Context, tenantID, month string, micros int64) error {
	if m.spent == nil {
		m.spent = make(map[string]int64)
	}
	m.spent[tenantID+":"+month] += micros
	return nil
}

func TestChatInternal(t *testing.T) {
	var events []analytics.UsageEvent
	ledger := &memLedger{spent: make(map[string]int64)}
	now := time.Now()

	e := setup(t, mockllm.Mode{}, mockllm.Mode{},
		analytics.UsageStage(func(ev analytics.UsageEvent) bool {
			events = append(events, ev)
			return true
		}),
		budget.NewStage(ledger, func() time.Time { return now }),
	)

	req := &api.ChatRequest{
		Model:    "default",
		Messages: []api.Message{{Role: "user", Content: api.Content{Text: "test internal"}}},
	}

	resp, err := e.h.ChatInternal(context.Background(), "default", req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Contains(t, resp.Choices[0].Message.Content.PlainText(), "echo: test internal")

	// Verify usage event kind is "judge" and tenant is "proofgate-system"
	require.Len(t, events, 1)
	ev := events[0]
	require.Equal(t, "judge", ev.Kind)
	require.Equal(t, "proofgate-system", ev.TenantID)
	require.Equal(t, "default", ev.Route)
	require.Equal(t, "a/small", ev.Target)
	require.Greater(t, ev.CostMicros, int64(0))

	// Verify cost was charged to "proofgate-system" in ledger
	month := budget.Month(now)
	spent, err := ledger.Spent(context.Background(), "proofgate-system", month)
	require.NoError(t, err)
	require.Equal(t, ev.CostMicros, spent)
}
