package server

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/health"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/provider"
	"github.com/proofgate/proofgate/internal/router"
)

func outcome(err error) health.Outcome {
	var pe *provider.Error
	switch {
	case err == nil:
		return health.OK
	case errors.As(err, &pe):
		if pe.Status == 400 || pe.Status == 404 || pe.Status == 413 || pe.Status == 422 {
			return health.Unknown // the request was bad, not the provider
		}
		return health.Failed
	case errors.Is(err, context.Canceled):
		return health.Unknown
	default:
		return health.Failed
	}
}

func (h *Handlers) observe(s health.Sample) {
	if h.Health != nil {
		h.Health.Observe(s)
	}
}

func planFor(rt *Runtime, c *pipeline.Call) []router.Target {
	if c.Target.Provider != "" && c.Target.Model != "" {
		plan := []router.Target{c.Target}
		for _, t := range rt.Router.Plan(c.Route) {
			if t != c.Target {
				plan = append(plan, t)
			}
		}
		return rt.Router.Order(plan)
	}
	return rt.Router.Plan(c.Route)
}

// price fills Usage (estimated if the provider sent none) and CostMicros.
func price(rt *Runtime, c *pipeline.Call, completionText string) {
	if c.Response != nil && c.Response.Usage != nil {
		c.Usage = *c.Response.Usage
	} else {
		prompt := c.Request.EstimatePromptTokens()
		comp := api.EstimateTokens(completionText)
		c.Usage = api.Usage{PromptTokens: prompt, CompletionTokens: comp, TotalTokens: prompt + comp}
		c.Values["usage.estimated"] = true
	}
	if cost, ok := rt.Pricing.CostMicros(c.Target.String(), c.Usage); ok {
		c.CostMicros = cost
	} else {
		c.Values["cost.unpriced"] = true
	}
}

// execChat runs a non-streaming call over the route's plan. Route.Timeout bounds the whole call.
func (h *Handlers) execChat(ctx context.Context, rt *Runtime, c *pipeline.Call) error {
	ctx, cancel := context.WithTimeout(ctx, c.Route.Timeout)
	defer cancel()
	plan := planFor(rt, c)
	fn := func(ctx context.Context, t router.Target) (*api.ChatResponse, error) {
		p, ok := rt.Registry.Get(t.Provider)
		if !ok {
			return nil, api.NoHealthyTarget()
		}
		start := time.Now()
		resp, err := p.Chat(ctx, t.Model, c.Request)
		atomic.AddInt64((*int64)(&c.UpstreamTime), int64(time.Since(start)))
		s := health.Sample{Target: t, Outcome: outcome(err), Gen: time.Since(start)}
		if err == nil && resp != nil && resp.Usage != nil {
			s.Tokens = resp.Usage.CompletionTokens
		}
		h.observe(s)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}

	var (
		resp   *api.ChatResponse
		res    router.Result
		hedged bool
		err    error
	)
	if c.Route.Hedge.Enabled && len(plan) >= 2 {
		b := h.hedgeBudget(c.Route)
		b.Request()
		defDelay := 150 * time.Millisecond
		if c.Route.Hedge.Delay > 0 {
			defDelay = c.Route.Hedge.Delay
		}
		delay := defDelay
		if h.Health != nil {
			delay = h.Health.HedgeDelay(plan[0], defDelay)
		}
		resp, res, hedged, err = router.ExecuteHedged[*api.ChatResponse](
			ctx, plan, c.Route.Retry, h.Breakers, delay,
			func() bool {
				allowed := b.Allow()
				if allowed && h.Metrics != nil {
					h.Metrics.ObserveHedge(c.Route.Name, "launched")
				}
				return allowed
			},
			fn,
			nil,
		)
		if hedged {
			c.Header.Set("X-ProofGate-Hedged", "true")
			if h.Metrics != nil {
				if err == nil {
					if res.Target == plan[0] {
						h.Metrics.ObserveHedge(c.Route.Name, "lost")
					} else {
						h.Metrics.ObserveHedge(c.Route.Name, "won")
					}
				}
			}
		}
	} else {
		res, err = router.Execute(ctx, plan, c.Route.Retry, h.Breakers, func(ctx context.Context, t router.Target) error {
			r, e := fn(ctx, t)
			if e == nil {
				resp = r
			}
			return e
		})
	}

	c.Target, c.Attempts = res.Target, res.Attempts
	if err != nil {
		return err
	}
	c.Response = resp
	text := ""
	if len(c.Response.Choices) > 0 {
		text = c.Response.Choices[0].Message.Content.PlainText()
	}
	price(rt, c, text)
	return nil
}
