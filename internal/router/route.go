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

// Target identifies a concrete upstream API capability (e.g. Provider: "openai", Model: "gpt-4o").
type Target struct {
	Provider string
	Model    string
}

// String returns the normalized canonical target identifier ("provider/model").
func (t Target) String() string { return t.Provider + "/" + t.Model }

// ParseTarget attempts to unmarshal a canonical string identifier back into a Target struct.
// Model ids may themselves contain '/', so only the first one splits.
func ParseTarget(s string) (Target, bool) {
	p, m, ok := strings.Cut(s, "/")
	if !ok || p == "" || m == "" {
		return Target{}, false
	}
	return Target{Provider: p, Model: m}, true
}

// RetryPolicy defines the backoff and maximum retry constraints for a specific route.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
}

// Route represents a logical mapping from a client-provided name to a set of underlying Targets,
// along with execution strategies, timeout constraints, and pipeline capabilities (Caching, Guardrails).
type Route struct {
	Name              string
	Targets           []Target
	Strategy          string
	Retry             RetryPolicy
	Timeout           time.Duration
	Deadline          time.Duration
	FirstTokenTimeout time.Duration
	StreamIdleTimeout time.Duration
	Embeddings        bool
	Cache             config.CacheConfig
	Guard             config.GuardConfig
	SmartRoute        config.SmartRouteConfig
	Hedge             config.HedgeConfig
}

// Router maintains the immutable topological map of all valid models and route definitions.
// It is constructed at startup and queried concurrently by thousands of requests.
type Router struct {
	routes       map[string]*Route
	order        []*Route
	providers    map[string]bool
	pricing      map[string]config.Price
	capabilities map[string]Capability
	breakers     *Breakers
	degraded     func(Target) bool
}

// New constructs a Router instance from the active ProofGate YAML configuration.
func New(cfg *config.Config, br *Breakers) *Router {
	r := &Router{
		routes:       map[string]*Route{},
		providers:    map[string]bool{},
		pricing:      cfg.Pricing,
		capabilities: map[string]Capability{},
		breakers:     br,
	}
	for k, cap := range cfg.Capabilities {
		r.capabilities[k] = Capability{
			MaxContextTokens: cap.MaxContextTokens,
			SupportsVision:   cap.SupportsVision,
			SupportsTools:    cap.SupportsTools,
		}
	}
	for _, p := range cfg.Providers {
		r.providers[p.Name] = true
	}
	for _, rc := range cfg.Routes {
		deadline := rc.Deadline
		if deadline <= 0 {
			deadline = 90 * time.Second
		}
		ftt := rc.FirstTokenTimeout
		if ftt <= 0 {
			ftt = 15 * time.Second
		}
		rt := &Route{Name: rc.Name, Strategy: rc.Strategy, Timeout: rc.Timeout, Deadline: deadline, FirstTokenTimeout: ftt, StreamIdleTimeout: rc.StreamIdleTimeout,
			Retry: RetryPolicy{MaxAttempts: rc.Retry.MaxAttempts, BaseDelay: rc.Retry.BaseDelay}, Embeddings: rc.Embeddings,
			Cache: rc.Cache, Guard: rc.Guard, SmartRoute: rc.SmartRoute, Hedge: rc.Hedge}
		for _, t := range rc.Targets {
			rt.Targets = append(rt.Targets, Target{Provider: t.Provider, Model: t.Model})
		}
		r.routes[rt.Name] = rt
		r.order = append(r.order, rt)
	}
	return r
}

// Routes returns the slice of all defined routes, maintaining stable configuration order.
func (r *Router) Routes() []*Route { return r.order }

// Targets returns a deduplicated list of all concrete upstream models referenced by any route.
func (r *Router) Targets() []Target {
	var targets []Target
	seen := make(map[Target]bool)
	for _, rt := range r.order {
		for _, tg := range rt.Targets {
			if !seen[tg] {
				seen[tg] = true
				targets = append(targets, tg)
			}
		}
	}
	return targets
}

// Resolve maps the requested model string to an executable Route plan.
//
// Resolution Logic:
// 1. Exact match against defined route alias in configuration (e.g. "prod-chat").
// 2. Passthrough target if `allowDirect` is granted via API Key (e.g. "openai/gpt-4o").
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
			Deadline: 90 * time.Second, FirstTokenTimeout: 15 * time.Second,
			StreamIdleTimeout: 30 * time.Second, Embeddings: true}, nil
	}
	return nil, api.BadRequest(fmt.Sprintf("unknown route %q", model))
}

// blendedPrice computes a weighted pricing heuristic.
// It assumes a 3:1 Input to Output token ratio, which is common for conversational AI traffic.
func (r *Router) blendedPrice(t Target) float64 {
	p := r.pricing[t.String()]
	return (3*p.Input + p.Output) / 4
}

// SetHealth binds the dynamic telemetry Tracker to the Router so that planning can
// actively deprioritize degraded upstream targets.
func (r *Router) SetHealth(degraded func(Target) bool) { r.degraded = degraded }

// SetCapabilities injects dynamic capability metadata (MaxTokens, Vision, Tools) into the routing engine.
func (r *Router) SetCapabilities(caps map[string]Capability) {
	r.capabilities = caps
}

// Order segregates and sorts the target plan into three tiers:
// 1. Healthy targets (fast path).
// 2. Degraded/Slow targets (soft failover).
// 3. Open Breakers (last resort hard failover).
func (r *Router) Order(plan []Target) []Target {
	var healthy, slow, open []Target
	for _, t := range plan {
		switch {
		case r.breakers != nil && r.breakers.open(t):
			open = append(open, t)
		case r.degraded != nil && r.degraded(t):
			slow = append(slow, t)
		default:
			healthy = append(healthy, t)
		}
	}
	return append(append(healthy, slow...), open...)
}

// Plan constructs the final ordered execution slice for a request.
//
// Process:
// 1. Base targets extracted from the resolved Route.
// 2. Strict Capability Filtering: Removes targets that cannot fulfill the request payload (e.g. Missing Vision support).
// 3. Strategy Sorting (e.g. "cheapest" orders by blended fiat cost).
// 4. Health Segregation (via `Order`): Demotes degraded providers to the end of the line.
func (r *Router) Plan(rt *Route, req ...*api.ChatRequest) []Target {
	var request *api.ChatRequest
	if len(req) > 0 {
		request = req[0]
	}

	var plan []Target
	for _, t := range rt.Targets {
		if request != nil && len(r.capabilities) > 0 {
			if cap, ok := r.capabilities[t.String()]; ok {
				if !Compatible(request, cap) {
					continue
				}
			}
		}
		plan = append(plan, t)
	}

	// If all targets were filtered out by strict capability checks, fall back to unfiltered
	// targets so the request still attempts execution and can surface a clearer upstream 400 error.
	if len(plan) == 0 {
		plan = append([]Target(nil), rt.Targets...)
	}

	if rt.Strategy == "cheapest" {
		sort.SliceStable(plan, func(i, j int) bool { return r.blendedPrice(plan[i]) < r.blendedPrice(plan[j]) })
	}
	return r.Order(plan)
}
