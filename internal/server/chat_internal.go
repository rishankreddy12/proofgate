package server

import (
	"context"
	"net/http"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/pipeline"
)

// ChatInternal dispatches an internal chat request (e.g. from the proof judge).
// It runs under the "proofgate-system" principal, sets X-ProofGate-Internal to bypass
// cache and ratelimit, tracks cost, and emits usage with kind="judge".
func (h *Handlers) ChatInternal(ctx context.Context, route string, req *api.ChatRequest) (*api.ChatResponse, error) {
	rt := h.State.Load()
	r, err := rt.Router.Resolve(route, true)
	if err != nil {
		return nil, err
	}
	p := auth.Principal{
		TenantID:    "proofgate-system",
		KeyID:       "internal-system",
		AllowDirect: true,
	}
	c := pipeline.NewCall(p, req, r)
	hdr := make(http.Header)
	hdr.Set("X-ProofGate-Internal", "true")
	c.Incoming = hdr
	c.Values["usage.kind"] = "judge"

	handled, err := h.Pipeline.Before(ctx, c)
	if err == nil && !handled {
		err = upstreamError(h.execChat(ctx, rt, c))
	}
	c.Err = err
	c.Latency = time.Since(c.Start)
	h.Pipeline.After(ctx, c)

	if err != nil {
		return nil, err
	}
	return c.Response, nil
}
