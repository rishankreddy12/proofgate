// Package budget prices calls and enforces monthly tenant budgets.
package budget

import (
	"math"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/config"
)

type Pricing struct {
	m map[string]config.Price
}

func NewPricing(m map[string]config.Price) *Pricing { return &Pricing{m: m} }

// CostMicros returns the call cost in micro-USD. Prices are USD per 1M tokens, so micros = tokens × price.
func (p *Pricing) CostMicros(target string, u api.Usage) (int64, bool) {
	pr, ok := p.m[target]
	if !ok {
		return 0, false
	}
	cached := u.CachedTokens()
	cachedPrice := pr.CachedInput
	if cachedPrice == 0 {
		cachedPrice = pr.Input
	}
	v := float64(u.PromptTokens-cached)*pr.Input + float64(cached)*cachedPrice + float64(u.CompletionTokens)*pr.Output
	return int64(math.Round(v)), true
}
