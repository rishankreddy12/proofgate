// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/store"
)

// ApplyOverrides dynamically mutates an immutable application Config snapshot using
// a set of live overrides retrieved from the distributed KV store (e.g., etcd/Redis).
// This enables the Admin Control Plane to alter critical runtime parameters (like cache
// strategies or smart routing enablement) instantly across all stateless gateway nodes
// without requiring rolling restarts or YAML configuration redeployments.
// It strictly isolates the mutation to a deep copy to guarantee thread safety.
func ApplyOverrides(cfg *config.Config, ovs []store.Override) (*config.Config, []error) {
	out := *cfg

	// Perform a deep copy of the Routes array. We do not need a full deep copy of the entire
	// config tree since overrides currently only target route-level specifications.
	out.Routes = make([]config.RouteConfig, len(cfg.Routes))
	copy(out.Routes, cfg.Routes)

	// Build an O(1) index of route names to their slice indices for fast patching.
	idx := map[string]int{}
	for i, r := range out.Routes {
		idx[r.Name] = i
	}

	var errs []error
	for _, o := range ovs {
		i, ok := idx[o.Route]
		if !ok {
			errs = append(errs, fmt.Errorf("override for unknown route %q", o.Route))
			continue
		}

		r := &out.Routes[i]
		switch o.Key {
		case "cache.mode":
			if o.Value != "off" && o.Value != "shadow" && o.Value != "on" {
				errs = append(errs, fmt.Errorf("route %q: bad cache.mode %q", o.Route, o.Value))
				continue
			}
			r.Cache.Mode = o.Value

		case "cache.threshold":
			v, err := strconv.ParseFloat(o.Value, 64)
			if err != nil || v <= 0 || v > 1 {
				errs = append(errs, fmt.Errorf("route %q: bad cache.threshold %q", o.Route, o.Value))
				continue
			}
			r.Cache.Threshold = v

		case "smart_route.mode":
			if o.Value != "off" && o.Value != "shadow" && o.Value != "on" {
				errs = append(errs, fmt.Errorf("route %q: bad smart_route.mode %q", o.Route, o.Value))
				continue
			}
			r.SmartRoute.Mode = o.Value

		case "cache.audit_sample_rate":
			v, err := strconv.ParseFloat(o.Value, 64)
			if err != nil || v < 0 || v > 1 {
				errs = append(errs, fmt.Errorf("route %q: bad cache.audit_sample_rate %q", o.Route, o.Value))
				continue
			}
			r.Cache.AuditSampleRate = v

		default:
			errs = append(errs, fmt.Errorf("route %q: unknown override key %q", o.Route, o.Key))
		}
	}
	return &out, errs
}

// OverrideSource defines the interface for fetching the current state of dynamic overrides
// from a persistence tier (usually SQL or Redis).
type OverrideSource interface {
	Overrides(ctx context.Context) ([]store.Override, error)
}

// PollOverrides operates a background polling loop that periodically synchronizes
// node-local state with the global configuration database.
// When differences are detected, it invokes the apply callback to hot-swap the
// immutable *server.Runtime pointer via atomic load/store instructions.
func PollOverrides(ctx context.Context, src OverrideSource, every time.Duration, apply func([]store.Override)) {
	tick := func() {
		ovs, err := src.Overrides(ctx)
		if err != nil {
			slog.Warn("override poll failed; keeping current runtime", "err", err)
			return
		}
		apply(ovs)
	}

	// Execute an immediate initial synchronization before falling into the ticker loop
	tick()

	t := time.NewTicker(every)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
