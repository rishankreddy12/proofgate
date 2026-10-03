// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/budget"
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
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, h.maxBody())).Decode(&req); err != nil {
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

	// Phase: Fiat Budget verification
	if h.Ledger != nil && p.Tenant.BudgetMicros() > 0 {
		if spent, err := h.Ledger.Spent(ctx, p.TenantID, budget.Month(h.Now())); err == nil && spent >= p.Tenant.BudgetMicros() {
			ev.Err = api.BudgetExceeded("monthly budget reached")
			api.WriteError(w, ev.Err)
			return
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
			resp, err := pr.Embed(ctx, t.Model, &req)
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
		if e := upstreamError(err); e != nil {
			api.WriteError(w, e)
		}
		return
	}

	tokens := out.Usage.PromptTokens
	cost, _ := rt.Pricing.CostMicros(ev.Target, api.Usage{PromptTokens: tokens})

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

	// Charge the fiat budget ledger
	if h.Ledger != nil && cost > 0 {
		_ = h.Ledger.Add(bg, p.TenantID, budget.Month(h.Now()), cost)
	}
}
