// Package server holds the HTTP handlers.
package server

import (
	"sync/atomic"

	"github.com/proofgate/proofgate/internal/budget"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/provider"
	"github.com/proofgate/proofgate/internal/router"
)

// Runtime is an immutable snapshot built from one config. Hot reload swaps the whole snapshot,
// so a request never sees a router from one config and a registry from another.
type Runtime struct {
	Config   *config.Config
	Router   *router.Router
	Registry *provider.Registry
	Pricing  *budget.Pricing
}

func BuildRuntime(cfg *config.Config, br *router.Breakers, getenv func(string) string) (*Runtime, error) {
	reg, err := provider.NewRegistry(cfg.ProviderSpecs(getenv))
	if err != nil {
		return nil, err
	}
	return &Runtime{Config: cfg, Router: router.New(cfg, br), Registry: reg, Pricing: budget.NewPricing(cfg.Pricing)}, nil
}

type State struct {
	p atomic.Pointer[Runtime]
}

func (s *State) Load() *Runtime   { return s.p.Load() }
func (s *State) Store(r *Runtime) { s.p.Store(r) }
