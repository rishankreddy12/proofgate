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

// ApplyOverrides returns a deep-enough copy of cfg (routes copied) with overrides applied.
func ApplyOverrides(cfg *config.Config, ovs []store.Override) (*config.Config, []error) {
	out := *cfg
	out.Routes = make([]config.RouteConfig, len(cfg.Routes))
	copy(out.Routes, cfg.Routes)
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
		default:
			errs = append(errs, fmt.Errorf("route %q: unknown override key %q", o.Route, o.Key))
		}
	}
	return &out, errs
}

type OverrideSource interface {
	Overrides(ctx context.Context) ([]store.Override, error)
}

// PollOverrides calls apply with the current overrides every interval (and once at start).
func PollOverrides(ctx context.Context, src OverrideSource, every time.Duration, apply func([]store.Override)) {
	tick := func() {
		ovs, err := src.Overrides(ctx)
		if err != nil {
			slog.Warn("override poll failed; keeping current runtime", "err", err)
			return
		}
		apply(ovs)
	}
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
