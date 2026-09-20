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

type EmbedEvent struct {
	Principal  auth.Principal
	Route      string
	Target     string
	Tokens     int
	CostMicros int64
	Duration   time.Duration
	Err        error
}

func (h *Handlers) Embeddings(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rt := h.State.Load()
	p, _ := auth.FromContext(r.Context())
	var req api.EmbeddingRequest
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
	ev := EmbedEvent{Principal: p, Route: route.Name}
	defer func() {
		ev.Duration = time.Since(start)
		if h.OnEmbed != nil {
			h.OnEmbed(ev)
		}
	}()
	est := 0
	for _, in := range req.Input {
		est += api.EstimateTokens(in)
	}
	ctx := r.Context()
	charged := false
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
	if h.Ledger != nil && p.Tenant.BudgetMicros() > 0 {
		if spent, err := h.Ledger.Spent(ctx, p.TenantID, budget.Month(h.Now())); err == nil && spent >= p.Tenant.BudgetMicros() {
			ev.Err = api.BudgetExceeded("monthly budget reached")
			api.WriteError(w, ev.Err)
			return
		}
	}
	var out *api.EmbeddingResponse
	res, err := router.Execute(ctx, rt.Router.Plan(route), route.Retry, h.Breakers,
		func(ctx context.Context, t router.Target) error {
			pr, ok := rt.Registry.Get(t.Provider)
			if !ok {
				return api.NoHealthyTarget()
			}
			resp, err := pr.Embed(ctx, t.Model, &req)
			if err == nil {
				out = resp
			}
			return err
		})
	ev.Target = res.Target.String()
	bg := context.WithoutCancel(ctx)
	if err != nil {
		ev.Err = err
		if charged {
			_ = h.Limiter.Adjust(bg, p.TenantID, p.Tenant, est)
		}
		if e := upstreamError(err); e != nil {
			api.WriteError(w, e)
		}
		return
	}
	tokens := out.Usage.PromptTokens
	cost, _ := rt.Pricing.CostMicros(ev.Target, api.Usage{PromptTokens: tokens})
	ev.Tokens, ev.CostMicros = tokens, cost
	h2 := w.Header()
	h2.Set("X-ProofGate-Request-Id", uuid.NewString())
	h2.Set("X-ProofGate-Route", route.Name)
	h2.Set("X-ProofGate-Target", ev.Target)
	h2.Set("X-ProofGate-Attempts", strconv.Itoa(res.Attempts))
	h2.Set("X-ProofGate-Cost-USD", usd(cost))
	writeJSON(w, http.StatusOK, out)
	if charged {
		_ = h.Limiter.Adjust(bg, p.TenantID, p.Tenant, est-tokens)
	}
	if h.Ledger != nil && cost > 0 {
		_ = h.Ledger.Add(bg, p.TenantID, budget.Month(h.Now()), cost)
	}
}
