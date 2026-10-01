// Package app provides the application-level wiring for the ProofGate gateway.
// BuildPipeline is the single source of truth for pipeline stage order.
package app

import (
	"time"

	"github.com/proofgate/proofgate/internal/agentrun"
	"github.com/proofgate/proofgate/internal/analytics"
	"github.com/proofgate/proofgate/internal/budget"
	"github.com/proofgate/proofgate/internal/cache"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/guard"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/ratelimit"
	"github.com/proofgate/proofgate/internal/telemetry"
)

// Deps bundles the concrete dependencies that each pipeline stage needs.
// main.go constructs these once and passes them here.
type Deps struct {
	Metrics        *telemetry.Metrics
	UsageEmit      func(analytics.UsageEvent) bool
	Runs           agentrun.Store
	FuzzyDetector  *agentrun.FuzzyDetector
	DefaultMaxToks int
	CacheStage     *cache.Stage
	Limiter        ratelimit.Backend
	MaxTokReserve  int
	FailOpenInc    func()
	Ledger         budget.Ledger
	Pricing        *budget.Pricing
	BudgetCfg      config.BudgetConfig
}

// StageOrder defines the canonical pipeline stage names in execution order.
// After hooks run in reverse. This list is the single source of truth used by both
// production wiring and the order assertion test.
var StageOrder = []string{
	"metrics",
	"trace",
	"usage",
	"agentrun",
	"ratelimit",
	"budget",
	"guard",
	"cache",
}

// BuildPipeline constructs the production pipeline with the canonical stage order.
// This must be the only place where pipeline.New is called with the full stage list.
func BuildPipeline(d Deps) *pipeline.Pipeline {
	return pipeline.New(
		d.Metrics.Stage(),
		telemetry.TraceStage(),
		analytics.UsageStage(d.UsageEmit),
		agentrun.NewStageWithLimiter(d.Runs, d.DefaultMaxToks, d.Limiter, d.FuzzyDetector),
		ratelimit.NewStage(d.Limiter, d.MaxTokReserve, d.DefaultMaxToks, d.FailOpenInc),
		budget.NewStage(d.Ledger, time.Now, budget.StageOpts{
			Pricing:        d.Pricing,
			ReserveStrict:  d.BudgetCfg.IsReserveStrict(),
			RequirePricing: d.BudgetCfg.IsRequirePricing(),
			DefaultMax:     d.DefaultMaxToks,
		}),
		guard.NewStage(),
		d.CacheStage,
	)
}
