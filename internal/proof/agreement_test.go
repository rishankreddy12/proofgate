package proof

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type mockPairedStore struct {
	judge []bool
	human []bool
}

func (m *mockPairedStore) HumanAndJudge(_ context.Context, _ string) ([]bool, []bool, error) {
	return m.judge, m.human, nil
}

func TestAgreementGate_InsufficientPairs(t *testing.T) {
	ctx := context.Background()
	// 30 pairs (< 50)
	j := make([]bool, 30)
	h := make([]bool, 30)
	for i := 0; i < 30; i++ {
		j[i] = true
		h[i] = true
	}

	store := &mockPairedStore{judge: j, human: h}
	res, err := CheckAgreement(ctx, store, "faq", 50, 0.60)
	require.NoError(t, err)
	require.False(t, res.OK)
	require.Equal(t, 30, res.Pairs)
	require.Contains(t, res.Message, "insufficient paired labels (need >= 50, have 30)")
}

func TestAgreementGate_LowAgreement_Blocks(t *testing.T) {
	ctx := context.Background()
	// 100 pairs with low agreement:
	// 40 both yes, 10 both no, 25 j-yes/h-no, 25 j-no/h-yes
	// Po = 50/100 = 0.50
	// P(j-yes) = 65/100, P(h-yes) = 65/100
	// Pe = 0.65*0.65 + 0.35*0.35 = 0.4225 + 0.1225 = 0.545
	// Kappa = (0.50 - 0.545) / (1 - 0.545) < 0
	// Let's create pairs with kappa ~ 0.35:
	// e.g. 50 both yes, 20 both no, 15 j-yes/h-no, 15 j-no/h-yes -> Po = 0.70
	// Pe = (65/100)*(65/100) + 0.35*0.35 = 0.545 -> kappa = (0.70 - 0.545)/0.455 ≈ 0.34
	var j, h []bool
	for i := 0; i < 50; i++ {
		j = append(j, true)
		h = append(h, true)
	}
	for i := 0; i < 20; i++ {
		j = append(j, false)
		h = append(h, false)
	}
	for i := 0; i < 15; i++ {
		j = append(j, true)
		h = append(h, false)
	}
	for i := 0; i < 15; i++ {
		j = append(j, false)
		h = append(h, true)
	}

	store := &mockPairedStore{judge: j, human: h}
	res, err := CheckAgreement(ctx, store, "faq", 50, 0.60)
	require.NoError(t, err)
	require.False(t, res.OK)
	require.InDelta(t, 0.34, res.Kappa, 0.05)
	require.Contains(t, res.Message, "judge agreement too low")
	require.Contains(t, res.Message, "threshold auto-apply blocked")
}

func TestAgreementGate_HighAgreement_Passes(t *testing.T) {
	ctx := context.Background()
	// High agreement:
	// 60 both yes, 30 both no, 5 j-yes/h-no, 5 j-no/h-yes -> 100 pairs
	// Po = 90/100 = 0.90
	// P(j-yes) = 65/100, P(h-yes) = 65/100
	// Pe = 0.545 -> kappa = (0.90 - 0.545)/0.455 ≈ 0.78 >= 0.60
	var j, h []bool
	for i := 0; i < 60; i++ {
		j = append(j, true)
		h = append(h, true)
	}
	for i := 0; i < 30; i++ {
		j = append(j, false)
		h = append(h, false)
	}
	for i := 0; i < 5; i++ {
		j = append(j, true)
		h = append(h, false)
	}
	for i := 0; i < 5; i++ {
		j = append(j, false)
		h = append(h, true)
	}

	store := &mockPairedStore{judge: j, human: h}
	res, err := CheckAgreement(ctx, store, "faq", 50, 0.60)
	require.NoError(t, err)
	require.True(t, res.OK)
	require.GreaterOrEqual(t, res.Kappa, 0.60)
	require.Empty(t, res.Message)
}
