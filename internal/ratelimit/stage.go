package ratelimit

import (
	"context"
	"log/slog"
	"strings"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/pipeline"
)

const chargedKey = "ratelimit.charged"

type Stage struct {
	b          Backend
	reserve    int
	defaultMax int
	onFailOpen func()
}

func NewStage(b Backend, reserve, defaultMax int, onFailOpen func()) *Stage {
	if onFailOpen == nil {
		onFailOpen = func() {}
	}
	return &Stage{b: b, reserve: reserve, defaultMax: defaultMax, onFailOpen: onFailOpen}
}

func (s *Stage) Name() string { return "ratelimit" }

func (s *Stage) Before(ctx context.Context, c *pipeline.Call) (bool, error) {
	if c.Incoming != nil && c.Incoming.Get("X-ProofGate-Internal") == "true" {
		return false, nil
	}
	p := c.Principal.Tenant
	if p.RPM == 0 && p.TPM == 0 {
		return false, nil
	}
	est := c.Request.EstimatePromptTokens() + min(c.Request.EffectiveMaxTokens(s.defaultMax), s.reserve)
	d, err := s.b.Take(ctx, c.Principal.TenantID, p, est)
	if err != nil {
		if p.Strict {
			return false, &api.Error{Status: 503, Message: "rate limiter unavailable", Type: "api_error", Code: "ratelimit_unavailable"}
		}
		slog.Warn("rate limiter unavailable, failing open", "tenant", c.Principal.TenantID, "err", err)
		s.onFailOpen()
		return false, nil
	}
	if d.TooLarge {
		return false, api.RateLimited("request_too_large", 0)
	}
	if !d.Allowed {
		return false, api.RateLimited("rate_limited", d.RetryAfter)
	}
	c.Values[chargedKey] = est
	return false, nil
}

// After replaces the estimate with real usage. Cache hits and failed calls consumed no upstream tokens.
func (s *Stage) After(ctx context.Context, c *pipeline.Call) {
	charged, ok := c.Values[chargedKey].(int)
	if !ok {
		return
	}
	actual := c.Usage.TotalTokens
	if strings.HasPrefix(c.CacheStatus, "hit") || (c.Err != nil && actual == 0) {
		actual = 0
	}
	if err := s.b.Adjust(context.WithoutCancel(ctx), c.Principal.TenantID, c.Principal.Tenant, charged-actual); err != nil {
		slog.Warn("rate limit reconcile failed", "tenant", c.Principal.TenantID, "err", err)
	}
}
