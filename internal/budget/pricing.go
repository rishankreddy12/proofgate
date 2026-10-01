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

// EstimateMaxMicros returns the maximum possible cost in micro-USD across all candidate targets.
// It uses the most expensive target in the route plan.
// If targets is empty or any target lacks a price, it returns (0, false).
// Prices in config are USD per 1M tokens, so micros = tokens × price.
func (p *Pricing) EstimateMaxMicros(targets []string, promptTokens, maxTokens int) (int64, bool) {
	if p == nil || len(targets) == 0 {
		return 0, false
	}
	var maxMicros int64
	for _, target := range targets {
		pr, ok := p.m[target]
		if !ok {
			return 0, false
		}
		v := float64(promptTokens)*pr.Input + float64(maxTokens)*pr.Output
		est := int64(math.Ceil(v))
		if est > maxMicros {
			maxMicros = est
		}
	}
	return maxMicros, true
}

// HasPricing returns true if pricing is configured for target.
func (p *Pricing) HasPricing(target string) bool {
	if p == nil {
		return false
	}
	_, ok := p.m[target]
	return ok
}
