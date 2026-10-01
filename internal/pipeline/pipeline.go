// Package pipeline runs ordered stages around each chat call.
package pipeline

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/router"
)

// Call is the state of one chat request as it moves through stages and the router.
type Call struct {
	ID           string
	Start        time.Time
	Principal    auth.Principal
	Request      *api.ChatRequest
	Route        *router.Route
	Stream       bool
	Response       *api.ChatResponse // set by the upstream call or by a short-circuiting stage
	ClientResponse *api.ChatResponse // client view (e.g. with PII restored); if set, written to client
	Target         router.Target
	Attempts     int
	Usage        api.Usage
	CostMicros   int64
	CacheStatus  string // miss | hit-exact | hit-semantic | bypass | shadow
	TTFT         time.Duration
	Latency      time.Duration
	UpstreamTime time.Duration // time spent waiting on providers; Latency-UpstreamTime = gateway overhead
	Err          error
	Internal     bool           // true ONLY for internal calls (embeddings, judge, shadow, fuzzy); never client-controlled
	Header       http.Header    // extra response headers set by stages
	Incoming     http.Header    // incoming request headers from the client
	Values       map[string]any // per-stage scratch space, keys prefixed with the stage name
	ran          int
}

func NewCall(p auth.Principal, req *api.ChatRequest, rt *router.Route) *Call {
	return &Call{ID: uuid.NewString(), Start: time.Now(), Principal: p, Request: req, Route: rt,
		Stream: req.Stream, CacheStatus: "miss", Header: http.Header{}, Values: map[string]any{}}
}

type Stage interface {
	Name() string
	Before(ctx context.Context, c *Call) (handled bool, err error)
	After(ctx context.Context, c *Call)
}

// Responder is an optional interface implemented by stages that transform
// the canonical response into a client view (e.g. restoring PII) before writing.
type Responder interface {
	Respond(ctx context.Context, c *Call)
}

type Pipeline struct {
	stages []Stage
}

func New(stages ...Stage) *Pipeline { return &Pipeline{stages: stages} }

func (p *Pipeline) Names() []string {
	out := make([]string, len(p.stages))
	for i, s := range p.stages {
		out[i] = s.Name()
	}
	return out
}

func (p *Pipeline) Before(ctx context.Context, c *Call) (bool, error) {
	for _, s := range p.stages {
		c.ran++
		handled, err := s.Before(ctx, c)
		if err != nil {
			c.Err = err
			return false, err
		}
		if handled {
			return true, nil
		}
	}
	return false, nil
}

func (p *Pipeline) Respond(ctx context.Context, c *Call) {
	if p == nil {
		return
	}
	for i := len(p.stages) - 1; i >= 0; i-- {
		if r, ok := p.stages[i].(Responder); ok {
			r.Respond(ctx, c)
		}
	}
}

func (p *Pipeline) After(ctx context.Context, c *Call) {
	for i := c.ran - 1; i >= 0; i-- {
		p.stages[i].After(ctx, c)
	}
}
