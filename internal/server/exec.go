package server

import (
	"context"
	"errors"
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
	res, err := router.Execute(ctx, planFor(rt, c), c.Route.Retry, h.Breakers,
		func(ctx context.Context, t router.Target) error {
			p, ok := rt.Registry.Get(t.Provider)
			if !ok {
				return api.NoHealthyTarget()
			}
			start := time.Now()
			resp, err := p.Chat(ctx, t.Model, c.Request)
			c.UpstreamTime += time.Since(start)
			s := health.Sample{Target: t, Outcome: outcome(err), Gen: time.Since(start)}
			if err == nil && resp != nil && resp.Usage != nil {
				s.Tokens = resp.Usage.CompletionTokens
			}
			h.observe(s)
			if err != nil {
				return err
			}
			c.Response = resp
			return nil
		})
	c.Target, c.Attempts = res.Target, res.Attempts
	if err != nil {
		return err
	}
	text := ""
	if len(c.Response.Choices) > 0 {
		text = c.Response.Choices[0].Message.Content.PlainText()
	}
	price(rt, c, text)
	return nil
}
