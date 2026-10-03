// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/pipeline"
)

// ChatInternal bypasses the external HTTP interface to dispatch a direct chat request.
// It operates exclusively under the elevated "proofgate-system" principal context,
// allowing sub-systems (like the semantic consensus Proof Judge) to securely query models
// without incurring public rate limits, cache pollution, or external ledger billing.
// It forcibly injects the X-ProofGate-Internal header to signal bypass semantics
// deep within the pipeline stages.
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
	c.Internal = true

	hdr := make(http.Header)
	hdr.Set("X-ProofGate-Internal", "true")
	c.Incoming = hdr
	c.Values["usage.kind"] = "judge"

	// Execute Pre-flight
	handled, err := h.Pipeline.Before(ctx, c)
	if err == nil && !handled {
		// Execute Upstream
		err = upstreamError(h.execChat(ctx, rt, c))
	}

	c.Err = err
	c.Latency = time.Since(c.Start)

	// Execute Post-flight
	h.Pipeline.After(ctx, c)

	if err != nil {
		return nil, err
	}
	return c.Response, nil
}
