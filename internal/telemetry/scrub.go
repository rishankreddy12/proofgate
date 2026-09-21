package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/proofgate/proofgate/internal/guard"
)

type Scrubber struct {
	inner   slog.Handler
	secrets *atomic.Pointer[[]string] // shared by handlers derived with WithAttrs/WithGroup
}

func NewScrubber(inner slog.Handler) *Scrubber {
	p := &atomic.Pointer[[]string]{}
	initList := []string{}
	p.Store(&initList)
	return &Scrubber{inner: inner, secrets: p}
}

func (s *Scrubber) Register(secret string) {
	if len(secret) < 8 {
		return
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

func (s *Scrubber) Enabled(ctx context.Context, l slog.Level) bool { return s.inner.Enabled(ctx, l) }

func (s *Scrubber) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, s.scrub(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(s.attr(a))
		return true
	})
	return s.inner.Handle(ctx, out)
}

func (s *Scrubber) WithAttrs(as []slog.Attr) slog.Handler {
	scrubbed := make([]slog.Attr, len(as))
	for i, a := range as {
		scrubbed[i] = s.attr(a)
	}
	return &Scrubber{inner: s.inner.WithAttrs(scrubbed), secrets: s.secrets}
}

func (s *Scrubber) WithGroup(name string) slog.Handler {
	return &Scrubber{inner: s.inner.WithGroup(name), secrets: s.secrets}
}
