package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/budget"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/ratelimit"
	"github.com/proofgate/proofgate/internal/router"
)

const maxBody = 10 << 20

type Handlers struct {
	State    *State
	Breakers *router.Breakers
	Pipeline *pipeline.Pipeline
	Limiter  ratelimit.Backend // used directly by the embeddings handler
	Ledger   budget.Ledger     // used directly by the embeddings handler
	Now      func() time.Time
	OnEmbed  func(EmbedEvent) // optional metrics hook
}

func decodeChat(r *http.Request) (*api.ChatRequest, error) {
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

// resolveRoute applies the key's route permissions.
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

func (h *Handlers) Chat(w http.ResponseWriter, r *http.Request) {
	rt := h.State.Load()
	p, _ := auth.FromContext(r.Context())
	req, err := decodeChat(r)
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
	ctx := r.Context()
	handled, err := h.Pipeline.Before(ctx, c)
	if err == nil && req.Stream {
		h.serveStream(w, r, rt, c, handled)
		return
	}
	if err == nil && !handled {
		err = upstreamError(h.execChat(ctx, rt, c))
		if err == nil && ctx.Err() != nil {
			c.Err = ctx.Err() // client left; nothing to write
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
		w.Header().Set("X-ProofGate-Cost-USD", usd(c.CostMicros))
		writeJSON(w, http.StatusOK, c.Response)
	}
	h.Pipeline.After(ctx, c)
}

func (h *Handlers) serveStream(w http.ResponseWriter, r *http.Request, rt *Runtime, c *pipeline.Call, handled bool) {
	api.WriteError(w, api.BadRequest("streaming arrives in Task 17"))
}
func (h *Handlers) Embeddings(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotImplemented) }
func (h *Handlers) Models(w http.ResponseWriter, r *http.Request)     { w.WriteHeader(http.StatusNotImplemented) }

type EmbedEvent struct{}
