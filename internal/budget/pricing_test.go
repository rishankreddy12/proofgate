package budget

import (
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCostMicros(t *testing.T) {
	p := NewPricing(map[string]config.Price{
		"a/m": {Input: 3, Output: 15, CachedInput: 0.3},
		"b/m": {Input: 1, Output: 2},
	})
	// 1000 prompt (200 cached) and 500 completion: 800*3 + 200*0.3 + 500*15 = 2400 + 60 + 7500
	c, ok := p.CostMicros("a/m", api.Usage{PromptTokens: 1000, CompletionTokens: 500,
		PromptTokensDetails: &api.PromptTokensDetails{CachedTokens: 200}})
	require.True(t, ok)
	require.EqualValues(t, 9960, c)

	c, _ = p.CostMicros("b/m", api.Usage{PromptTokens: 10, CompletionTokens: 10,
		PromptTokensDetails: &api.PromptTokensDetails{CachedTokens: 10}})
	require.EqualValues(t, 30, c, "no cached price: cached tokens billed at input price")

	_, ok = p.CostMicros("zzz/m", api.Usage{PromptTokens: 1})
	require.False(t, ok)
}
