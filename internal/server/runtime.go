// Package server holds the HTTP presentation layer and request-lifecycle handlers.
package server

import (
	"sync/atomic"

	"github.com/proofgate/proofgate/internal/budget"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/mcpproxy"
	"github.com/proofgate/proofgate/internal/provider"
	"github.com/proofgate/proofgate/internal/router"
)

// Runtime encapsulates an immutable, point-in-time snapshot of the entire gateway's
// configuration state. This includes the materialized routing tree, provider registry,
// active pricing charts, and MCP configurations.
//
// Concurrency Design: The runtime is never mutated in place. When dynamic configuration
// overrides are pulled from the control plane, a brand new Runtime object is constructed
// and swapped into the global `State` container via an atomic pointer store.
// This guarantees that a single in-flight HTTP request will never read a fragmented configuration
// (e.g., routing to a model that no longer exists in the registry).
type Runtime struct {
	Config   *config.Config
	Router   *router.Router
	Registry *provider.Registry
	Pricing  *budget.Pricing
	MCP      map[string]mcpproxy.Upstream
}

// ResolveMCP translates the declarative MCP server YAML configurations into concrete
// mcpproxy.Upstream objects. It dynamically injects required secrets (like API keys)
// into the upstream HTTP headers by evaluating them against the environment via getenv.
func ResolveMCP(cfg *config.Config, getenv func(string) string) map[string]mcpproxy.Upstream {
	out := map[string]mcpproxy.Upstream{}
	for _, s := range cfg.MCPServers {
		h := map[string]string{}
		for name, env := range s.Headers {
			h[name] = getenv(env) // Expand secret variable references
		}
		out[s.Name] = mcpproxy.Upstream{Name: s.Name, URL: s.URL, Headers: h}
	}
	return out
}

// BuildRuntime acts as the primary factory function for assembling a new Runtime snapshot.
// It parses the raw YAML Config structures and spins up the heavy downstream components
// like the provider adapters (via NewRegistry) and the resilient router tree (via New).
// It throws an error if any of the configurations are structurally invalid (e.g., missing API keys).
func BuildRuntime(cfg *config.Config, br *router.Breakers, getenv func(string) string, keys func(provider string) provider.KeyFunc) (*Runtime, error) {
	reg, err := provider.NewRegistry(cfg.ProviderSpecs(getenv, keys))
	if err != nil {
		return nil, err
	}
	return &Runtime{
		Config:   cfg,
		Router:   router.New(cfg, br),
		Registry: reg,
		Pricing:  budget.NewPricing(cfg.Pricing),
		MCP:      ResolveMCP(cfg, getenv),
	}, nil
}

// State operates as a thread-safe container for the active Runtime snapshot.
// It uses `atomic.Pointer` to facilitate zero-downtime hot-reloading.
type State struct {
	p atomic.Pointer[Runtime]
}

// Load retrieves the currently active Runtime snapshot for an incoming request.
// Once loaded into a local variable within a handler, the request is guaranteed
// consistency against that snapshot regardless of background reloads.
func (s *State) Load() *Runtime { return s.p.Load() }

// Store performs an atomic pointer swap, replacing the active configuration across
// the entire gateway instantly. Existing in-flight requests continue executing against
// their old pointer reference, which is garbage collected once they finish.
func (s *State) Store(r *Runtime) { s.p.Store(r) }
