// Package mcpproxy proxies Model Context Protocol servers with per-key tool policies, run limits and auditing.
package mcpproxy

import (
	"path"

	"github.com/proofgate/proofgate/internal/store"
)

func match(globs []string, name string) bool {
	for _, g := range globs {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

// DefaultAllowedMethods lists the methods allowed by default per the MCP specification.
// Other method families (such as resources/*, prompts/*, sampling/*) must be explicitly allowed.
var DefaultAllowedMethods = []string{
	"initialize",
	"ping",
	"tools/list",
	"tools/call",
	"notifications/*",
}

// Allowed executes the primary logic for the Allowed operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func Allowed(p *store.MCPPolicy, server, tool string) bool {
	if p == nil {
		return false
	}
	sp, ok := p.Servers[server]
	if !ok || match(sp.Deny, tool) {
		return false
	}
	return match(sp.Allow, tool)
}

// MethodAllowed checks if the given MCP method is permitted by the key's policy or defaults.
func MethodAllowed(p *store.MCPPolicy, server, method string) bool {
	if p != nil {
		if sp, ok := p.Servers[server]; ok && len(sp.AllowMethods) > 0 {
			return match(sp.AllowMethods, method)
		}
		if len(p.AllowMethods) > 0 {
			return match(p.AllowMethods, method)
		}
	}
	return match(DefaultAllowedMethods, method)
}
