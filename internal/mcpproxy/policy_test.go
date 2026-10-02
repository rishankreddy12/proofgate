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

func TestMethodAllowed(t *testing.T) {
	// Defaults allow initialize, ping, tools/list, tools/call, notifications/*
	require.True(t, MethodAllowed(nil, "github", "initialize"))
	require.True(t, MethodAllowed(nil, "github", "ping"))
	require.True(t, MethodAllowed(nil, "github", "tools/list"))
	require.True(t, MethodAllowed(nil, "github", "tools/call"))
	require.True(t, MethodAllowed(nil, "github", "notifications/message"))
	// Defaults deny resources/*, prompts/*, sampling/*
	require.False(t, MethodAllowed(nil, "github", "resources/read"))
	require.False(t, MethodAllowed(nil, "github", "resources/list"))
	require.False(t, MethodAllowed(nil, "github", "prompts/get"))
	require.False(t, MethodAllowed(nil, "github", "sampling/createMessage"))

	// Configured server policy can allow additional methods
	p := &store.MCPPolicy{Servers: map[string]store.MCPServerPolicy{
		"github": {AllowMethods: []string{"tools/*", "resources/*"}},
	}}
	require.True(t, MethodAllowed(p, "github", "resources/read"))
	require.True(t, MethodAllowed(p, "github", "tools/call"))
	require.False(t, MethodAllowed(p, "github", "prompts/get"))
}
