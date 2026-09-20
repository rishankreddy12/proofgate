package server

import (
	"context"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/router"
)

// EmbedInternal embeds texts through a configured embeddings route, with the route's failover rules.
// The cache stage and (in Plan 3) the smart router use it; it does not apply tenant limits.
func (h *Handlers) EmbedInternal(ctx context.Context, route string, inputs []string) ([][]float32, api.Usage, router.Target, error) {
	rt := h.State.Load()
	r, err := rt.Router.Resolve(route, false)
	if err != nil {
		return nil, api.Usage{}, router.Target{}, err
	}
	var out *api.EmbeddingResponse
	res, err := router.Execute(ctx, rt.Router.Plan(r), r.Retry, h.Breakers, func(ctx context.Context, t router.Target) error {
		p, ok := rt.Registry.Get(t.Provider)
		if !ok {
			return api.NoHealthyTarget()
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
	vecs := make([][]float32, len(out.Data))
	for _, d := range out.Data {
		vecs[d.Index] = d.Embedding
	}
	return vecs, out.Usage, res.Target, nil
}
