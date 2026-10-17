// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/budget"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/guard"
	"github.com/proofgate/proofgate/internal/router"
)

// EmbedEvent defines the shape of a structured telemetry event emitted upon completion
// of a vector embedding request. Used for metrics aggregations and cost tracking.
type EmbedEvent struct {
	Principal  auth.Principal
	Route      string
	Target     string
	Tokens     int
	CostMicros int64
	Duration   time.Duration
	Err        error
}

// Embeddings is the primary HTTP handler for the /v1/embeddings endpoint.
// It manages the complete lifecycle of generating dense vector representations for strings.
//
// Lifecycle sequence:
// 1. Validates the JSON payload and strictly caps payload sizes via http.MaxBytesReader.
// 2. Pre-computes a heuristic token estimate (length/4) to tentatively deduct tokens from the tenant's rate limiter.
// 3. Verifies the tenant's fiat ledger budget (if configured) to prevent quota overruns.
// 4. Executes the upstream embedding call via the resiliency router (circuit breakers & failovers).
// 5. Restores/adjusts the exact token usage in the rate limiter and permanently charges the fiat ledger.
// 6. Responds with the vectors, decorating headers with latency, cost, and routing traces.
func (h *Handlers) Embeddings(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rt := h.State.Load()
	p, _ := auth.FromContext(r.Context())

	var req api.EmbeddingRequest
	maxBody := h.maxBody()
	if rt != nil && rt.Config != nil && rt.Config.Server.MaxRequestBodyBytes > 0 {
		maxBody = rt.Config.Server.MaxRequestBodyBytes
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody)).Decode(&req); err != nil {
		api.WriteError(w, api.BadRequest("invalid JSON"))
		return
	}

	if req.Model == "" || len(req.Input) == 0 {
		api.WriteError(w, api.BadRequest("model and input are required"))
		return
	}

	route, err := resolveRoute(rt, p, req.Model, true)
	if err != nil {
		api.WriteError(w, err)
		return
	}

	upstreamReq, piiRedacted := redactEmbeddingRequest(req, route.Guard.PII)
	if piiRedacted > 0 {
		w.Header().Set("X-ProofGate-PII-Redacted", strconv.Itoa(piiRedacted))
	}

	ev := EmbedEvent{Principal: p, Route: route.Name}

	// Ensure telemetry event is emitted regardless of panic or failure.
	defer func() {
		ev.Duration = time.Since(start)
		if h.OnEmbed != nil {
			h.OnEmbed(ev)
		}
	}()

	// Pre-charge heuristics: Calculate estimated tokens for rate-limiting before the network call.
	est := 0
	for _, in := range req.Input {
		est += api.EstimateTokens(in)
	}

	ctx := r.Context()
	charged := false

	// Phase: Rate Limiting
	if h.Limiter != nil && (p.Tenant.RPM > 0 || p.Tenant.TPM > 0) {
		d, err := h.Limiter.Take(ctx, p.TenantID, p.Tenant, est)
		switch {
		case err != nil && p.Tenant.Strict:
			ev.Err = err
			api.WriteError(w, &api.Error{Status: 503, Message: "rate limiter unavailable", Type: "api_error", Code: "ratelimit_unavailable"})
			return
		case err == nil && d.TooLarge:
			ev.Err = api.RateLimited("request_too_large", 0)
			api.WriteError(w, ev.Err)
			return
		case err == nil && !d.Allowed:
			ev.Err = api.RateLimited("rate_limited", d.RetryAfter)
			api.WriteError(w, ev.Err)
			return
		case err == nil:
			charged = true
		}
	}

	// Phase: Fiat Budget verification and atomic reservation
	budgetLimit := p.Tenant.BudgetMicros()
	budgetMonth := budget.Month(h.Now())
	var reservedCost int64
	hasBudgetReservation := false

	if h.Ledger != nil && budgetLimit > 0 {
		targets := make([]string, 0, len(route.Targets))
		for _, t := range route.Targets {
			targets = append(targets, t.String())
		}
		if len(targets) == 0 && req.Model != "" {
			targets = []string{req.Model}
		}

		isStrict := rt.Config == nil || rt.Config.Budget.IsReserveStrict()
		requirePricing := rt.Config == nil || rt.Config.Budget.IsRequirePricing()

		if rt.Pricing != nil && len(targets) > 0 {
			estCost, priced := rt.Pricing.EstimateMaxMicros(targets, est, 0)
			if !priced && requirePricing {
				ev.Err = &api.Error{
					Status:  500,
					Message: fmt.Sprintf("unpriced target in embeddings route for tenant %s with monthly budget", p.TenantID),
					Type:    "api_error",
					Code:    "unpriced_target",
				}
				api.WriteError(w, ev.Err)
				return
			}
			reservedCost = estCost
		}

		if isStrict {
			ok, spent, err := h.Ledger.Reserve(ctx, p.TenantID, budgetMonth, reservedCost, budgetLimit)
			if err != nil {
				if p.Tenant.Strict {
					ev.Err = &api.Error{Status: 503, Message: "budget ledger unavailable", Type: "api_error", Code: "budget_unavailable"}
					api.WriteError(w, ev.Err)
					return
				}
				slog.Warn("embeddings budget ledger unavailable, failing open", "tenant", p.TenantID, "err", err)
			} else if !ok {
				ev.Err = api.BudgetExceeded(fmt.Sprintf("monthly budget of $%.2f reached", float64(budgetLimit)/1e6))
				api.WriteError(w, ev.Err)
				return
			} else {
				hasBudgetReservation = true
				w.Header().Set("X-ProofGate-Budget-Remaining-USD", fmt.Sprintf("%.6f", max(0, float64(budgetLimit-spent)/1e6)))
			}
		} else {
			spent, err := h.Ledger.Spent(ctx, p.TenantID, budgetMonth)
			if err != nil {
				if p.Tenant.Strict {
					ev.Err = &api.Error{Status: 503, Message: "budget ledger unavailable", Type: "api_error", Code: "budget_unavailable"}
					api.WriteError(w, ev.Err)
					return
				}
				slog.Warn("embeddings budget ledger unavailable, failing open", "tenant", p.TenantID, "err", err)
			} else if spent >= budgetLimit {
				ev.Err = api.BudgetExceeded(fmt.Sprintf("monthly budget of $%.2f reached", float64(budgetLimit)/1e6))
				api.WriteError(w, ev.Err)
				return
			}
		}
	}

	var out *api.EmbeddingResponse
	deadline := route.Deadline
	if deadline <= 0 {
		deadline = 90 * time.Second
	}

	// Phase: Upstream Execution with Resiliency
	res, err := router.Execute(ctx, rt.Router.Plan(route), route.Retry, h.Breakers, deadline,
		func(ctx context.Context, t router.Target) error {
			pr, ok := rt.Registry.Get(t.Provider)
			if !ok {
				return api.NoHealthyTarget()
			}
			if route.Timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, route.Timeout)
				defer cancel()
			}
			resp, err := pr.Embed(ctx, t.Model, &upstreamReq)
			if err == nil {
				out = resp
			}
			return err
		})

	ev.Target = res.Target.String()

	// We use context.WithoutCancel to ensure that token refunds and ledger debits
	// successfully execute even if the client disconnects immediately after the upstream call completes.
	bg := context.WithoutCancel(ctx)

	if err != nil {
		ev.Err = err
		if charged {
			_ = h.Limiter.Adjust(bg, p.TenantID, p.Tenant, est) // Refund the heuristic pre-charge
		}
		if hasBudgetReservation && reservedCost > 0 {
			if rErr := h.Ledger.Add(bg, p.TenantID, budgetMonth, -reservedCost); rErr != nil {
				slog.Error("embeddings budget refund failed", "tenant", p.TenantID, "err", rErr)
			}
		}
		if e := upstreamError(err); e != nil {
			api.WriteError(w, e)
		}
		return
	}

	tokens := out.Usage.PromptTokens
	cost, priced := rt.Pricing.CostMicros(ev.Target, api.Usage{PromptTokens: tokens})
	requirePricing := rt.Config == nil || rt.Config.Budget.IsRequirePricing()
	if !priced && requirePricing && rt.Pricing != nil {
		if charged {
			_ = h.Limiter.Adjust(bg, p.TenantID, p.Tenant, est-tokens)
		}
		if hasBudgetReservation && reservedCost > 0 {
			if rErr := h.Ledger.Add(bg, p.TenantID, budgetMonth, -reservedCost); rErr != nil {
				slog.Error("embeddings budget refund failed", "tenant", p.TenantID, "err", rErr)
			}
		}
		ev.Err = &api.Error{Status: 500, Message: "unpriced embeddings target", Type: "api_error", Code: "unpriced_target"}
		api.WriteError(w, ev.Err)
		return
	}

	ev.Tokens = tokens
	ev.CostMicros = cost

	// Phase: Output Formatting and Accounting
	h2 := w.Header()
	h2.Set("X-ProofGate-Request-Id", uuid.NewString())
	h2.Set("X-ProofGate-Route", route.Name)
	h2.Set("X-ProofGate-Target", ev.Target)
	h2.Set("X-ProofGate-Attempts", strconv.Itoa(res.Attempts))
	h2.Set("X-ProofGate-Cost-USD", usd(cost))

	writeJSON(w, http.StatusOK, out)

	// True-up the rate limit bucket with the exact usage vs the estimate
	if charged {
		_ = h.Limiter.Adjust(bg, p.TenantID, p.Tenant, est-tokens)
	}

	// Reconcile or charge the fiat budget ledger
	if h.Ledger != nil {
		if hasBudgetReservation {
			delta := cost - reservedCost
			if delta != 0 {
				if rErr := h.Ledger.Add(bg, p.TenantID, budgetMonth, delta); rErr != nil {
					slog.Error("embeddings budget reconcile failed", "tenant", p.TenantID, "delta", delta, "err", rErr)
				}
			}
		} else if cost > 0 {
			if rErr := h.Ledger.Add(bg, p.TenantID, budgetMonth, cost); rErr != nil {
				slog.Error("embeddings budget charge failed", "tenant", p.TenantID, "cost", cost, "err", rErr)
			}
		}
	}
}

// redactEmbeddingRequest returns an isolated request for upstream execution. The decoded
// request remains unchanged so accounting, telemetry, and later middleware retain the caller's
// original input while providers only receive the guarded text.
func redactEmbeddingRequest(req api.EmbeddingRequest, cfg config.PIIGuardConfig) (api.EmbeddingRequest, int) {
	out := req
	out.Input = append(api.StringOrSlice(nil), req.Input...)
	if !cfg.Enabled {
		return out, 0
	}
	mode := cfg.Mode
	if mode == "" {
		mode = "redact"
	}
	numberer := make(guard.Numberer)
	redacted := 0
	for i, text := range req.Input {
		matches := guard.DetectPII(text)
		if len(matches) == 0 {
			continue
		}
		result := guard.Redact(text, matches, mode, numberer)
		out.Input[i] = result.Redacted
		redacted += len(matches)
	}
	return out, redacted
}
