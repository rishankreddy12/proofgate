package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBudgetMicros(t *testing.T) {
	require.EqualValues(t, 12_500_000, TenantPolicy{MonthlyBudgetUSD: 12.5}.BudgetMicros())
	require.EqualValues(t, 0, TenantPolicy{}.BudgetMicros())
}
