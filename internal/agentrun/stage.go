package agentrun

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/pipeline"
)

var RunIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

type stage struct{ s Store }

func NewStage(s Store) pipeline.Stage { return &stage{s: s} }

func (st *stage) Name() string { return "agentrun" }

func runErr(status int, code, msg string) *api.Error {
	t := "insufficient_quota"
	if status == 429 {
		t = "rate_limit_error"
	}
	return &api.Error{Status: status, Message: msg, Type: t, Code: code}
}

func (st *stage) Before(ctx context.Context, c *pipeline.Call) (bool, error) {
	pp := c.Principal.Key.Run
	if pp == nil {
		return false, nil
	}
	p := pp.WithDefaults()
	runID := ""
	if c.Incoming != nil {
		runID = c.Incoming.Get("X-ProofGate-Run-Id")
	}
	if runID == "" {
		if p.RequireRunID {
			return false, api.BadRequest("X-ProofGate-Run-Id is required for this key")
		}
		return false, nil
	}
	if !RunIDPattern.MatchString(runID) {
		return false, api.BadRequest("X-ProofGate-Run-Id must match " + RunIDPattern.String())
	}
	res, err := st.s.Step(ctx, c.Principal.TenantID, runID, p, Fingerprint(c.Request))
	if err != nil {
		if c.Principal.Tenant.Strict {
			return false, &api.Error{Status: 503, Message: "run store unavailable", Type: "api_error", Code: "run_store_unavailable"}
		}
		slog.Warn("run store unavailable, failing open", "run", runID, "err", err)
		return false, nil
	}
	switch res.Status {
	case StepsExceeded:
		return false, runErr(402, "run_budget_exceeded", fmt.Sprintf("run %s reached max_steps (%d)", runID, p.MaxSteps))
	case CostExceeded:
		return false, runErr(402, "run_budget_exceeded", fmt.Sprintf("run %s reached max_cost_usd ($%.4f)", runID, p.MaxCostUSD))
	case TokensExceeded:
		return false, runErr(402, "run_budget_exceeded", fmt.Sprintf("run %s reached max_tokens (%d)", runID, p.MaxTokens))
	case Loop:
		return false, runErr(429, "agent_loop_detected",
			fmt.Sprintf("run %s repeated the same step %d times in its last %d steps", runID, res.Repeats, p.LoopWindow))
	}
	c.Values["run.id"] = runID
	c.Header.Set("X-ProofGate-Run-Steps", strconv.Itoa(res.Steps))
	if p.MaxCostUSD > 0 {
		c.Header.Set("X-ProofGate-Run-Remaining-USD", fmt.Sprintf("%.6f", float64(p.CostMicros()-res.CostMicros)/1e6))
	}
	return false, nil
}

func (st *stage) After(ctx context.Context, c *pipeline.Call) {
	runID, ok := c.Values["run.id"].(string)
	if !ok {
		return
	}
	p := c.Principal.Key.Run.WithDefaults()
	if err := st.s.Charge(context.WithoutCancel(ctx), c.Principal.TenantID, runID, p, c.CostMicros, c.Usage.TotalTokens); err != nil {
		slog.Warn("run charge failed", "run", runID, "err", err)
	}
}
