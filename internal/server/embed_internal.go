// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
package server

import (
	"context"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/router"
)

// EmbedInternal executes an internal vector embedding generation request bypassing standard HTTP constraints.
// It is heavily utilized by internal gateway subsystems (such as the Semantic Cache evaluation stage
// and the KNN Smart Router) to generate vector embeddings of user prompts in-band before forwarding
// the actual chat generation request.
//
// Because this executes internally, it completely bypasses tenant-level rate limiting (RPM/TPM) and
// ledger-based fiat budget constraints. However, it fully respects the failover, retry, and circuit-breaking
// rules configured on the underlying embedding route.
func (h *Handlers) EmbedInternal(ctx context.Context, route string, inputs []string) ([][]float32, api.Usage, router.Target, error) {
	rt := h.State.Load()
	r, err := rt.Router.Resolve(route, false)
	if err != nil {
		return nil, api.Usage{}, router.Target{}, err
	}

	deadline := r.Deadline
	if deadline <= 0 {
		deadline = 90 * time.Second
	}

	var out *api.EmbeddingResponse

	// Execute the request through the router's resiliency layer (breakers/failovers).
	res, err := router.Execute(ctx, rt.Router.Plan(r), r.Retry, h.Breakers, deadline, func(ctx context.Context, t router.Target) error {
		p, ok := rt.Registry.Get(t.Provider)
		if !ok {
			return api.NoHealthyTarget()
		}

		if r.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, r.Timeout)
			defer cancel()
		}

		resp, err := p.Embed(ctx, t.Model, &api.EmbeddingRequest{Input: inputs})
		if err == nil {
			out = resp
		}
		return err
	})

	if err != nil {
		return nil, api.Usage{}, res.Target, err
	}

	// Extract the flat float32 vectors mapped by index to guarantee ordering matches the input array.
	vecs := make([][]float32, len(out.Data))
	for _, d := range out.Data {
		vecs[d.Index] = d.Embedding
	}

	return vecs, out.Usage, res.Target, nil
}
