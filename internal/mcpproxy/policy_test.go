package mcpproxy

import (
	"testing"

	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

func TestAllowed(t *testing.T) {
	p := &store.MCPPolicy{Servers: map[string]store.MCPServerPolicy{
		"github": {Allow: []string{"get_*", "list_*", "create_issue"}, Deny: []string{"*_secret*"}},
	}}
	require.True(t, Allowed(p, "github", "get_file"))
	require.True(t, Allowed(p, "github", "create_issue"))
	require.False(t, Allowed(p, "github", "get_secret_value"), "deny wins")
	require.False(t, Allowed(p, "github", "delete_repo"), "not in allow list")
	require.False(t, Allowed(p, "slack", "get_file"), "no entry for server")
	require.False(t, Allowed(nil, "github", "get_file"), "no policy: deny")
}
