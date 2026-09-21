package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPServers(t *testing.T) {
	base := `
providers: [{name: a, type: openai, base_url: "http://a"}]
routes: [{name: r, targets: [{provider: a, model: m}]}]
`
	c, err := Parse([]byte(base + `
mcp_servers:
  - {name: github, url: "https://api.githubcopilot.com/mcp/", headers_env: {Authorization: GITHUB_MCP_AUTH}}
`))
	require.NoError(t, err)
	require.Equal(t, "GITHUB_MCP_AUTH", c.MCPServers[0].Headers["Authorization"])

	_, err = Parse([]byte(base + `
mcp_servers:
  - {name: github, url: "http://evil.example.com/mcp"}
`))
	require.ErrorContains(t, err, "https")

	_, err = Parse([]byte(base + `
mcp_insecure_hosts: [fake-mcp]
mcp_servers:
  - {name: fake, url: "http://fake-mcp:8000/mcp"}
`))
	require.NoError(t, err)
}
