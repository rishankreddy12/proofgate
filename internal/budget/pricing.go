// Package budget prices calls and enforces monthly tenant budgets.
package budget

import (
	"math"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/config"
)

// Pricing maintains an immutable matrix of token costs per provider target.
// It is safely instantiated during the global Runtime build phase and accessed
// concurrently by thousands of request goroutines.
type Pricing struct {
	m map[string]config.Price
}

// NewPricing constructs a Pricing oracle from the active Gateway configuration.
func NewPricing(m map[string]config.Price) *Pricing { return &Pricing{m: m} }

// CostMicros evaluates the precise fiat cost of a completed LLM generation.
//
// Math: Prices are defined in USD per 1 Million tokens. The function leverages Prompt/Completion
// usage and accounts for provider-side "Prompt Caching" discounts (CachedTokens) to calculate
// the absolute cost in micro-USD (millionths of a cent).
func (p *Pricing) CostMicros(target string, u api.Usage) (int64, bool) {
	pr, ok := p.m[target]
	if !ok {
		return 0, false
	}
	cached := u.CachedTokens()
	cachedPrice := pr.CachedInput
	if cachedPrice == 0 {
		cachedPrice = pr.Input // Fallback to standard input price if no discount specified
	}
	v := float64(u.PromptTokens-cached)*pr.Input + float64(cached)*cachedPrice + float64(u.CompletionTokens)*pr.Output
	return int64(math.Round(v)), true
}

// EstimateMaxMicros computes the theoretical worst-case fiat cost of a request *before*
// it executes. This is critical for pre-generation ledger reservations.
//
// Logic: It sweeps all eligible fallback targets in the routing plan, assumes the maximum
// possible output tokens (max_tokens) will be generated, and returns the maximum calculated cost.
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

// HasPricing verifies if a specific target model has cost parameters configured in the oracle.
func (p *Pricing) HasPricing(target string) bool {
	if p == nil {
		return false
	}
	_, ok := p.m[target]
	return ok
}
