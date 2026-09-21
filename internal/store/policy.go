package store

import (
	"math"
	"time"
)

// TenantPolicy limits a tenant. Zero values mean unlimited.
type TenantPolicy struct {
	RPM              int     `json:"rpm"`
	TPM              int     `json:"tpm"`
	MonthlyBudgetUSD float64 `json:"monthly_budget_usd"`
	Strict           bool    `json:"strict"` // fail closed when Redis is unavailable
}

func (p TenantPolicy) BudgetMicros() int64 { return int64(math.Round(p.MonthlyBudgetUSD * 1e6)) }

type RunPolicy struct {
	MaxCostUSD   float64       `json:"max_cost_usd"`
	MaxSteps     int           `json:"max_steps"`
	MaxTokens    int           `json:"max_tokens"`
	TTL          time.Duration `json:"ttl"`
	RequireRunID bool          `json:"require_run_id"`
	LoopRepeats  int           `json:"loop_repeats"`
	LoopWindow   int           `json:"loop_window"`
}

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
	return p
}

func (p RunPolicy) CostMicros() int64 { return int64(math.Round(p.MaxCostUSD * 1e6)) }

type MCPServerPolicy struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

type MCPPolicy struct {
	Servers map[string]MCPServerPolicy `json:"servers"`
}

// KeyPolicy holds per-key options.
type KeyPolicy struct {
	AllowDirect bool       `json:"allow_direct"`
	Run         *RunPolicy `json:"run,omitempty"`
	MCP         *MCPPolicy `json:"mcp,omitempty"`
}

