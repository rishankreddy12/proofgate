// Package pipeline runs ordered stages around each chat call.
// It acts as the interceptor chain mapping requests from the HTTP edge to the router
// and processing the response telemetry, caching, and guardrails on the way back out.
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

// Call represents the full lifecycle state of a single Chat Completion execution traversing the Gateway.
//
// Architecture:
// It acts as the "Context" object passed linearly through the Before() and After() hooks
// of every registered Pipeline Stage. It is not thread-safe.
type Call struct {
	ID              string
	Start           time.Time
	Principal       auth.Principal
	Request         *api.ChatRequest
	Route           *router.Route
	Stream          bool
	Response        *api.ChatResponse // set by the upstream call or by a short-circuiting stage (e.g. Cache)
	ClientResponse  *api.ChatResponse // client view (e.g. with PII restored); if set, written to client instead of Response
	Target          router.Target
	Attempts        int
	Usage           api.Usage
	CostMicros      int64
	HedgeCostMicros int64
	CacheStatus     string // miss | hit-exact | hit-semantic | bypass | shadow
	TTFT            time.Duration
	Latency         time.Duration
	UpstreamTime    time.Duration // time spent waiting on providers; Latency-UpstreamTime = gateway overhead
	Err             error
	Internal        bool           // true ONLY for internal calls (embeddings, judge, shadow, fuzzy); never client-controlled
	Header          http.Header    // extra response headers set by stages
	Incoming        http.Header    // incoming request headers from the client
	Values          map[string]any // per-stage scratch space, keys prefixed with the stage name
	ran             int            // tracks the index of the highest completed Before() stage
}

// NewCall instantiates a new Call context, typically invoked right after
// tenant authentication but before the first stage executes.
func NewCall(p auth.Principal, req *api.ChatRequest, rt *router.Route) *Call {
	return &Call{
		ID:          uuid.NewString(),
		Start:       time.Now(),
		Principal:   p,
		Request:     req,
		Route:       rt,
		Stream:      req.Stream,
		CacheStatus: "miss",
		Header:      http.Header{},
		Values:      map[string]any{},
	}
}

// Stage defines the contract for an interceptor within the LLM execution pipeline.
//
// Execution Order:
// 1. Before() is called in ascending order (e.g. RateLimit -> Cache -> Router).
// 2. If a Stage returns handled=true or an error, execution short-circuits.
// 3. After() is called in descending order for any stage that successfully completed Before().
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

// Pipeline orchestrates the execution of multiple Stages over a Call.
type Pipeline struct {
	stages []Stage
}

// New constructs a Pipeline from a variadic list of Stages. Order matters.
func New(stages ...Stage) *Pipeline { return &Pipeline{stages: stages} }

// Names returns the string identifiers of all registered stages in execution order.
func (p *Pipeline) Names() []string {
	out := make([]string, len(p.stages))
	for i, s := range p.stages {
		out[i] = s.Name()
	}
	return out
}

// Before executes the pre-flight logic of all registered stages.
//
// Circuit Breaking: If any stage returns an error or handled=true,
// execution immediately halts and returns to the caller.
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

// Respond executes the transformation logic of all registered Responder stages,
// in reverse order of their Before() execution.
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

// After executes the post-flight logic of all registered stages in LIFO order
// (Last-In-First-Out) strictly for the stages that successfully completed Before().
func (p *Pipeline) After(ctx context.Context, c *Call) {
	for i := c.ran - 1; i >= 0; i-- {
		p.stages[i].After(ctx, c)
	}
}
