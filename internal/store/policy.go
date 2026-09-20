package store

import "math"

// TenantPolicy limits a tenant. Zero values mean unlimited.
type TenantPolicy struct {
	RPM              int     `json:"rpm"`
	TPM              int     `json:"tpm"`
	MonthlyBudgetUSD float64 `json:"monthly_budget_usd"`
	Strict           bool    `json:"strict"` // fail closed when Redis is unavailable
}

func (p TenantPolicy) BudgetMicros() int64 { return int64(math.Round(p.MonthlyBudgetUSD * 1e6)) }

// KeyPolicy holds per-key options. Plan 4 adds Run and MCP policies.
type KeyPolicy struct {
	AllowDirect bool `json:"allow_direct"`
}
