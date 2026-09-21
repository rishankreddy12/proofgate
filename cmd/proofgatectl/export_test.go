package main

import (
	"testing"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRedactConfig(t *testing.T) {
	cfg, err := config.Parse([]byte(`
providers:
  - {name: a, type: openai, base_url: "https://api.openai.com/v1", api_key_env: OPENAI_API_KEY, headers: {X-Org: acme}}
routes: [{name: r, targets: [{provider: a, model: m}]}]
mcp_servers: [{name: gh, url: "https://x/mcp", headers_env: {Authorization: GH_TOKEN}}]
`))
	require.NoError(t, err)
	out := redactConfig(cfg)
	require.Equal(t, "<redacted>", out.Providers[0].APIKeyEnv)
	require.Equal(t, "acme", out.Providers[0].Headers["X-Org"], "non-secret headers are kept")
	require.Equal(t, "<redacted>", out.MCPServers[0].Headers["Authorization"])
	require.Equal(t, "OPENAI_API_KEY", cfg.Providers[0].APIKeyEnv, "input untouched")
}
