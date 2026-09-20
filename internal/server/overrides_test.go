package server

import (
	"testing"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

func TestApplyOverrides(t *testing.T) {
	cfg, err := config.Parse([]byte(`
providers: [{name: a, type: openai, base_url: "http://a"}]
routes:
  - {name: embed, embeddings: true, targets: [{provider: a, model: e}]}
  - name: faq
    cache: {mode: shadow, semantic: true, embedding_route: embed, threshold: 0.95}
    targets: [{provider: a, model: m}]
`))
	require.NoError(t, err)
	out, errs := ApplyOverrides(cfg, []store.Override{
		{Route: "faq", Key: "cache.mode", Value: "on"},
		{Route: "faq", Key: "cache.threshold", Value: "0.91"},
		{Route: "faq", Key: "cache.threshold", Value: "banana"},
		{Route: "nope", Key: "cache.mode", Value: "on"},
	})
	require.Len(t, errs, 2)
	require.Equal(t, "on", out.Routes[1].Cache.Mode)
	require.Equal(t, 0.91, out.Routes[1].Cache.Threshold)
	require.Equal(t, "shadow", cfg.Routes[1].Cache.Mode, "input config is not mutated")
}
