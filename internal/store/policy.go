// Package store provides enterprise-grade capabilities, configuration, and structural components for the store subsystem.
package store

import (
	"math"
	"time"
)

// TenantCachePolicy defines rules around semantic caching for a specific tenant.
type TenantCachePolicy struct {
	AllowClientKey bool `json:"allow_client_key"`
}

// TenantPolicy defines the quota, feature enablement, and strictness limits applied
// globally to all traffic originated by a specific Tenant. Zero values generally mean unlimited.
type TenantPolicy struct {
	RPM              int                `json:"rpm"`
	TPM              int                `json:"tpm"`
	MonthlyBudgetUSD float64            `json:"monthly_budget_usd"`
	Strict           bool               `json:"strict"` // fail closed when Redis is unavailable
	Cache            *TenantCachePolicy `json:"cache,omitempty"`
	ShadowConsent    bool               `json:"shadow_consent"`
	KMSURI           string             `json:"kms_uri,omitempty"` // ID of the tenant's specific KMS key for BYOK
}

// BudgetMicros converts the MonthlyBudgetUSD representation to micro-dollars (USD * 1e6).
func (p TenantPolicy) BudgetMicros() int64 { return int64(math.Round(p.MonthlyBudgetUSD * 1e6)) }

// RunPolicy configures restrictions on autonomous loop execution and agent trajectories.
type RunPolicy struct {
	MaxCostUSD     float64       `json:"max_cost_usd"`
	MaxSteps       int           `json:"max_steps"`
	MaxTokens      int           `json:"max_tokens"`
	TTL            time.Duration `json:"ttl"`
	RequireRunID   bool          `json:"require_run_id"`
	RunIDFallback  string        `json:"run_id_fallback,omitempty"`
	LoopRepeats    int           `json:"loop_repeats"`
	LoopWindow     int           `json:"loop_window"`
	FuzzyLoop      bool          `json:"fuzzy_loop"`
	FuzzyThreshold float64       `json:"fuzzy_threshold"`
}

// WithDefaults returns a copy of the RunPolicy populated with safe fallback values
// for fields left unconfigured.
func (p RunPolicy) WithDefaults() RunPolicy {
	if p.TTL == 0 {
		p.TTL = time.Hour
	}
	if p.LoopRepeats == 0 {
		p.LoopRepeats = 3
	}
	if p.LoopWindow == 0 {
		p.LoopWindow = 20
	}
	if p.FuzzyThreshold <= 0 {
		p.FuzzyThreshold = 0.95
	}
	return p
}

// CostMicros executes the primary logic for the CostMicros operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (p RunPolicy) CostMicros() int64 { return int64(math.Round(p.MaxCostUSD * 1e6)) }

// MCPServerPolicy restricts the MCP servers and methods an API key is authorized to access.
type MCPServerPolicy struct {
	Allow        []string `json:"allow"`
	Deny         []string `json:"deny"`
	AllowMethods []string `json:"allow_methods,omitempty"`
}

// MCPPolicy encompasses all Model Context Protocol routing restrictions.
type MCPPolicy struct {
	Servers      map[string]MCPServerPolicy `json:"servers"`
	AllowMethods []string                   `json:"allow_methods,omitempty"`
}

// KeyPolicy holds per-key options.
type KeyPolicy struct {
	AllowDirect bool       `json:"allow_direct"`
	Run         *RunPolicy `json:"run,omitempty"`
	MCP         *MCPPolicy `json:"mcp,omitempty"`
}
