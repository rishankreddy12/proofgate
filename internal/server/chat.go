// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/budget"
	"github.com/proofgate/proofgate/internal/health"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/ratelimit"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/proofgate/proofgate/internal/telemetry"
)

const defaultMaxBody = 10 << 20

// Handlers centralizes all state and dependencies required by the HTTP handlers.
// It encapsulates reference pointers to caching, rate-limiting, hedging, and
// routing subsystems, ensuring that the request lifecycle can be executed without
// relying on global singletons.
type Handlers struct {
	State               *State
	Breakers            *router.Breakers
	Pipeline            *pipeline.Pipeline
	Limiter             ratelimit.Backend // used directly by the embeddings handler
	Ledger              budget.Ledger     // used directly by the embeddings handler
	Now                 func() time.Time
	OnEmbed             func(EmbedEvent) // optional metrics hook
	Health              *health.Tracker
	Metrics             *telemetry.Metrics
	Hedges              sync.Map
	MCP                 http.Handler
	MaxRequestBodyBytes int64
}

// maxBody returns the configured maximum bytes for an incoming request payload,
// falling back to a safe 10MB default to prevent memory exhaustion attacks.
func (h *Handlers) maxBody() int64 {
	if h != nil && h.MaxRequestBodyBytes > 0 {
		return h.MaxRequestBodyBytes
	}
	return defaultMaxBody
}

// hedgeBudget retrieves or initializes the concurrency tokens (HedgeBudget) for a given route.
// This ensures that tail-latency hedging mechanisms do not overwhelm upstream providers
// by strictly capping the global ratio of in-flight hedged requests.
func (h *Handlers) hedgeBudget(r *router.Route) *router.HedgeBudget {
	if r == nil {
		return nil
	}
	if v, ok := h.Hedges.Load(r.Name); ok {
		return v.(*router.HedgeBudget)
	}
	maxExtra := r.Hedge.MaxExtra
	if maxExtra <= 0 {
		maxExtra = 0.10
	}
	b := router.NewHedgeBudget(maxExtra)
	v, _ := h.Hedges.LoadOrStore(r.Name, b)
	return v.(*router.HedgeBudget)
}

// decodeChat parses and validates the incoming JSON payload into an api.ChatRequest.
// It strictly enforces MaxBytesReader limits to mitigate slow-loris and OOM vectors.
func decodeChat(r *http.Request, maxBody int64) (*api.ChatRequest, error) {
	var req api.ChatRequest
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody)).Decode(&req); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, &api.Error{Status: 413, Message: "request body too large", Type: "invalid_request_error", Code: "request_too_large"}
		}
		return nil, api.BadRequest("invalid JSON: " + err.Error())
	}
	switch {
	case req.Model == "":
		return nil, api.BadRequest("model is required")
	case len(req.Messages) == 0:
		return nil, api.BadRequest("messages must not be empty")
	case req.N != nil && *req.N > 1:
		return nil, api.BadRequest("n > 1 is not supported")
	}
	return &req, nil
}

// resolveRoute verifies that the incoming request is authorized to traverse the
// requested routing path, evaluating tenant-level RBAC policies against the route schema.
func resolveRoute(rt *Runtime, p auth.Principal, model string, embeddings bool) (*router.Route, error) {
	route, err := rt.Router.Resolve(model, p.AllowDirect)
	if err != nil {
		return nil, err
	}
	if !p.CanUseRoute(route.Name) && !(p.AllowDirect && len(route.Targets) == 1 && route.Name == route.Targets[0].String()) {
		return nil, api.Forbidden("route_forbidden", "this key may not use route "+route.Name)
	}
	if route.Embeddings != embeddings && route.Name != route.Targets[0].String() {
		if embeddings {
			return nil, api.BadRequest("route " + route.Name + " does not serve embeddings")
		}
		return nil, api.BadRequest("route " + route.Name + " is an embeddings route")
	}
	return route, nil
}

var clientHeaders = []string{
	"X-ProofGate-Cache",
	"X-ProofGate-Cache-Tags",
	"X-ProofGate-Cache-Query",
	"X-ProofGate-Run-Id",
	"X-ProofGate-Target",
}

// sanitizeIncoming strips out potentially dangerous or sensitive internal HTTP headers,
// preserving only explicitly permitted ProofGate-specific control headers.
func sanitizeIncoming(h http.Header) http.Header {
	out := make(http.Header, len(clientHeaders))
	for _, k := range clientHeaders {
		if v := h.Values(k); len(v) > 0 {
			out[http.CanonicalHeaderKey(k)] = v
		}
	}
	return out
}

// Chat is the primary entrypoint for the /v1/chat/completions endpoint.
// It orchestrates the end-to-end request lifecycle:
// 1. Decodes and validates the payload.
// 2. Resolves route permissions.
// 3. Initializes the pipeline.Call structure.
// 4. Invokes the pre-flight pipeline stages (Auth, Cache, Guardrails).
// 5. Branches into streaming (serveStream) or monolithic (execChat) upstream execution.
// 6. Executes post-flight pipeline stages (Telemetry, Ledger).
func (h *Handlers) Chat(w http.ResponseWriter, r *http.Request) {
	rt := h.State.Load()
	p, _ := auth.FromContext(r.Context())

	req, err := decodeChat(r, h.maxBody())
	if err != nil {
		api.WriteError(w, err)
		return
	}

	route, err := resolveRoute(rt, p, req.Model, false)
	if err != nil {
		api.WriteError(w, err)
		return
	}

	c := pipeline.NewCall(p, req, route)
	if r.Header.Get("X-ProofGate-Internal") != "" {
		h.Metrics.ObserveSpoofedInternal(p.TenantID)
	}
	c.Incoming = sanitizeIncoming(r.Header)

	ctx := r.Context()

	// Execute Pipeline Before-Stage (e.g., semantic cache hit)
	handled, err := h.Pipeline.Before(ctx, c)

	if err == nil && req.Stream {
		// Hand off to the streaming responder
		h.serveStream(w, r, rt, c, handled)
		return
	}

	if err == nil && !handled {
		// Execute synchronous upstream network call
		err = upstreamError(h.execChat(ctx, rt, c))
		if err == nil && ctx.Err() != nil {
			c.Err = ctx.Err() // client disconnected prematurely
			h.Pipeline.After(ctx, c)
			return
		}
	}

	c.Err = err
	c.Latency = time.Since(c.Start)

	writeCallHeaders(w, c)
	if err != nil {
		api.WriteError(w, err)
	} else {
		h.Pipeline.Respond(ctx, c) // Execute Pipeline Respond-Stage
		respToWrite := c.Response
		if c.ClientResponse != nil {
			respToWrite = c.ClientResponse
		}
		w.Header().Set("X-ProofGate-Cost-USD", usd(c.CostMicros))
		writeJSON(w, http.StatusOK, respToWrite)
	}

	// Execute Pipeline After-Stage (e.g., write-behind cache, analytics emission)
	h.Pipeline.After(ctx, c)
}
