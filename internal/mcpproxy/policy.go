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
