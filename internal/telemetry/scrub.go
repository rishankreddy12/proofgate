// Package telemetry provides enterprise-grade capabilities, configuration, and structural components for the telemetry subsystem.
package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/proofgate/proofgate/internal/guard"
)

// Scrubber intercepts all structured log records emitted by `slog` and redacts
// sensitive information before it reaches standard output or external log aggregators.
//
// Redaction Capabilities:
// 1. Exact string matching (used for known API Keys and Tenant secrets).
// 2. Heuristic regex scanning (via `guard.Detect`) to strip inadvertently leaked credentials.
type Scrubber struct {
	inner   slog.Handler
	secrets *atomic.Pointer[[]string] // shared by handlers derived with WithAttrs/WithGroup
}

// NewScrubber wraps an existing slog.Handler (like JSONHandler) with redaction logic.
func NewScrubber(inner slog.Handler) *Scrubber {
	p := &atomic.Pointer[[]string]{}
	initList := []string{}
	p.Store(&initList)
	return &Scrubber{inner: inner, secrets: p}
}

// Register adds a highly-sensitive string (e.g. a newly loaded provider API key) to the
// internal Exact Match redaction dictionary. This operation uses compare-and-swap (CAS)
// for thread-safe mutation without locking.
func (s *Scrubber) Register(secret string) {
	if len(secret) < 8 {
		return // Reject trivially short strings to prevent catastrophic log mangling
	}
	for {
		old := s.secrets.Load()
		for _, v := range *old {
			if v == secret {
				return
			}
		}
		next := append(append([]string(nil), *old...), secret)
		if s.secrets.CompareAndSwap(old, &next) {
			return
		}
	}
}

// scrub sweeps a single string for both exactly registered secrets and heuristically identified credentials.
func (s *Scrubber) scrub(v string) string {
	for _, sec := range *s.secrets.Load() {
		v = strings.ReplaceAll(v, sec, "[REDACTED]")
	}
	spans := guard.Detect(v)
	for i := len(spans) - 1; i >= 0; i-- {
		if spans[i].Kind == guard.Secret {
			v = v[:spans[i].Start] + "[REDACTED:SECRET]" + v[spans[i].End:]
		}
	}
	return v
}

// attr recursively applies the scrubbing logic to nested slog.Attr pairs.
func (s *Scrubber) attr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, s.scrub(v.String()))
	case slog.KindGroup:
		attrs := v.Group()
		out := make([]any, len(attrs))
		for i, g := range attrs {
			out[i] = s.attr(g)
		}
		return slog.Group(a.Key, out...)
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			return slog.String(a.Key, s.scrub(x.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, s.scrub(x.String()))
		}
	}
	return a
}

// Enabled delegates the level check to the underlying handler.
func (s *Scrubber) Enabled(ctx context.Context, l slog.Level) bool { return s.inner.Enabled(ctx, l) }

// Handle intercepts the actual log record, scrubs the primary message, and walks all attributes.
func (s *Scrubber) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, s.scrub(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(s.attr(a))
		return true
	})
	return s.inner.Handle(ctx, out)
}

// WithAttrs forks the handler, applying the current scrub logic to the new pre-bound attributes.
func (s *Scrubber) WithAttrs(as []slog.Attr) slog.Handler {
	scrubbed := make([]slog.Attr, len(as))
	for i, a := range as {
		scrubbed[i] = s.attr(a)
	}
	return &Scrubber{inner: s.inner.WithAttrs(scrubbed), secrets: s.secrets}
}

// WithGroup forks the handler into a named log group.
func (s *Scrubber) WithGroup(name string) slog.Handler {
	return &Scrubber{inner: s.inner.WithGroup(name), secrets: s.secrets}
}
