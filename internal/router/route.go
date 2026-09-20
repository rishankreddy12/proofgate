// Package router turns a model name into an ordered plan of targets and executes it with retries and failover.
package router

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/config"
)

type Target struct {
	Provider string
	Model    string
}

func (t Target) String() string { return t.Provider + "/" + t.Model }

// ParseTarget splits "provider/model". Model ids may themselves contain '/', so only the first one splits.
func ParseTarget(s string) (Target, bool) {
	p, m, ok := strings.Cut(s, "/")
	if !ok || p == "" || m == "" {
		return Target{}, false
	}
	return Target{Provider: p, Model: m}, true
}

type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
}

type Route struct {
	Name              string
	Targets           []Target
	Strategy          string
	Retry             RetryPolicy
	Timeout           time.Duration
	StreamIdleTimeout time.Duration
	Embeddings        bool
}

type Router struct {
	routes    map[string]*Route
	order     []*Route
	providers map[string]bool
	pricing   map[string]config.Price
	breakers  *Breakers
}

func New(cfg *config.Config, br *Breakers) *Router {
	r := &Router{routes: map[string]*Route{}, providers: map[string]bool{}, pricing: cfg.Pricing, breakers: br}
	for _, p := range cfg.Providers {
		r.providers[p.Name] = true
	}
	for _, rc := range cfg.Routes {
		rt := &Route{Name: rc.Name, Strategy: rc.Strategy, Timeout: rc.Timeout, StreamIdleTimeout: rc.StreamIdleTimeout,
			Retry: RetryPolicy{MaxAttempts: rc.Retry.MaxAttempts, BaseDelay: rc.Retry.BaseDelay}, Embeddings: rc.Embeddings}
		for _, t := range rc.Targets {
			rt.Targets = append(rt.Targets, Target{Provider: t.Provider, Model: t.Model})
		}
		r.routes[rt.Name] = rt
		r.order = append(r.order, rt)
	}
	return r
}

func (r *Router) Routes() []*Route { return r.order }

// Resolve maps the request's model field to a route: a configured route name, or "provider/model"
// when the key allows direct targets.
func (r *Router) Resolve(model string, allowDirect bool) (*Route, error) {
	if rt, ok := r.routes[model]; ok {
		return rt, nil
	}
	if t, ok := ParseTarget(model); ok {
		if !allowDirect {
			return nil, api.Forbidden("route_forbidden", "direct targets are not allowed for this key; use a route name")
		}
		if !r.providers[t.Provider] {
			return nil, api.BadRequest(fmt.Sprintf("unknown provider %q", t.Provider))
		}
		return &Route{Name: model, Targets: []Target{t}, Strategy: "fallback",
			Retry: RetryPolicy{MaxAttempts: 2, BaseDelay: 200 * time.Millisecond}, Timeout: 120 * time.Second,
			StreamIdleTimeout: 30 * time.Second, Embeddings: true}, nil
	}
	return nil, api.BadRequest(fmt.Sprintf("unknown route %q", model))
}

// blendedPrice is a 3:1 input:output weighting, a common rough mix for chat traffic.
func (r *Router) blendedPrice(t Target) float64 {
	p := r.pricing[t.String()]
	return (3*p.Input + p.Output) / 4
}

// Plan orders targets by strategy, then moves targets with open breakers to the end.
func (r *Router) Plan(rt *Route) []Target {
	plan := append([]Target(nil), rt.Targets...)
	if rt.Strategy == "cheapest" {
		sort.SliceStable(plan, func(i, j int) bool { return r.blendedPrice(plan[i]) < r.blendedPrice(plan[j]) })
	}
	if r.breakers == nil {
		return plan
	}
	healthy := plan[:0:0]
	var open []Target
	for _, t := range plan {
		if r.breakers.open(t) {
			open = append(open, t)
		} else {
			healthy = append(healthy, t)
		}
	}
	return append(healthy, open...)
}
