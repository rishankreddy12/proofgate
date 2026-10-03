// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/health"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/provider"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/proofgate/proofgate/internal/sse"
)

// wantsUsage returns true if the client explicitly requested token usage telemetry
// in the final chunk of the SSE stream via the stream_options object.
func wantsUsage(req *api.ChatRequest) bool {
	return req.StreamOptions != nil && req.StreamOptions.IncludeUsage
}

// usageOnly returns true if the chunk contains only usage telemetry and no completion choices.
func usageOnly(c *api.ChatChunk) bool { return c.Usage != nil && len(c.Choices) == 0 }

// streamResult encapsulates the initial state of a successfully opened upstream connection.
type streamResult struct {
	stream provider.Stream
	first  *api.ChatChunk
	cancel context.CancelFunc
}

// openStream initiates an upstream streaming connection. It executes the router's fallback plan,
// attempts to connect to providers, and waits strictly for the First Token (TTFT).
// If the first token doesn't arrive within the FirstTokenTimeout, it cancels the connection
// and fails over to the next target in the plan.
func (h *Handlers) openStream(ctx context.Context, rt *Runtime, c *pipeline.Call) (provider.Stream, *api.ChatChunk, context.CancelFunc, error) {
	plan := planFor(rt, c)
	ftt := c.Route.FirstTokenTimeout
	if ftt <= 0 {
		ftt = 15 * time.Second
	}
	deadline := c.Route.Deadline
	if deadline <= 0 {
		deadline = 90 * time.Second
	}
	fn := func(attemptCtx context.Context, t router.Target) (streamResult, error) {
		p, ok := rt.Registry.Get(t.Provider)
		if !ok {
			return streamResult{}, api.NoHealthyTarget()
		}

		sctx, scancel := context.WithCancel(ctx)
		ttft := time.AfterFunc(ftt, scancel)
		stopAttemptWatch := make(chan struct{})
		defer close(stopAttemptWatch)

		// Ensure that if the router cancels this attempt (e.g. timeout or hedge loss),
		// the upstream connection is aborted.
		go func() {
			select {
			case <-attemptCtx.Done():
				scancel()
			case <-stopAttemptWatch:
			}
		}()

		start := time.Now()
		s, err := p.ChatStream(sctx, t.Model, c.Request)
		var first *api.ChatChunk
		if err == nil {
			first, err = s.Recv() // Block until TTFT
			if err != nil {
				s.Close()
			}
		}
		stoppedInTime := ttft.Stop()
		atomic.AddInt64((*int64)(&c.UpstreamTime), int64(time.Since(start)))

		if err == nil {
			h.observe(health.Sample{Target: t, TTFT: time.Since(start), Outcome: outcome(err)})
		} else {
			h.observe(health.Sample{Target: t, Outcome: outcome(err)})
		}

		if err != nil {
			scancel()
			if !stoppedInTime && attemptCtx.Err() == nil {
				return streamResult{}, &provider.Error{Provider: t.Provider, Status: 504, Message: "time to first token exceeded", Retryable: true}
			}
			if errors.Is(err, io.EOF) {
				return streamResult{}, &provider.Error{Provider: t.Provider, Status: 502, Message: "empty stream", Retryable: true}
			}
			return streamResult{}, err
		}
		return streamResult{stream: s, first: first, cancel: scancel}, nil
	}

	discard := func(res streamResult) {
		if res.cancel != nil {
			res.cancel()
		}
		if res.stream != nil {
			res.stream.Close()
		}
	}

	var (
		resResult streamResult
		res       router.Result
		hedged    bool
		err       error
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
		resResult, res, hedged, err = router.ExecuteHedged[streamResult](
			ctx, plan, c.Route.Retry, h.Breakers, deadline, delay,
			func() bool {
				allowed := b.Allow()
				if allowed && h.Metrics != nil {
					h.Metrics.ObserveHedge(c.Route.Name, "launched")
				}
				return allowed
			},
			fn,
			discard, // Close streams of the loser in the hedge race
		)
		if hedged {
			c.Header.Set("X-ProofGate-Hedged", "true")
			if h.Metrics != nil {
				if err == nil {
					wastedTokens := c.Request.EstimatePromptTokens()
					if res.Target == plan[0] {
						h.Metrics.ObserveHedge(c.Route.Name, "lost")
						h.Metrics.ObserveHedgeWaste(c.Route.Name, plan[1].String(), wastedTokens)
						if cost, ok := rt.Pricing.EstimateMaxMicros([]string{plan[1].String()}, wastedTokens, 0); ok {
							atomic.AddInt64(&c.HedgeCostMicros, cost)
						}
					} else {
						h.Metrics.ObserveHedge(c.Route.Name, "won")
						h.Metrics.ObserveHedgeWaste(c.Route.Name, plan[0].String(), wastedTokens)
						if cost, ok := rt.Pricing.EstimateMaxMicros([]string{plan[0].String()}, wastedTokens, 0); ok {
							atomic.AddInt64(&c.HedgeCostMicros, cost)
						}
					}
				}
			}
		}
	} else {
		res, err = router.Execute(ctx, plan, c.Route.Retry, h.Breakers, deadline, func(ctx context.Context, t router.Target) error {
			r, e := fn(ctx, t)
			if e == nil {
				resResult = r
			}
			return e
		})
	}

	c.Target, c.Attempts = res.Target, res.Attempts
	return resResult.stream, resResult.first, resResult.cancel, err
}

// serveStream orchestrates the lifecycle of an SSE response to the client.
// It bridges the gap between the provider's stream output and the client's HTTP response.
//
// If the request was satisfied by cache (handled=true), it artificially shreds the monolithic
// cached response back into a simulated chunk stream to remain transparent to the client.
// Otherwise, it manages the live pump loop, applies stream filtering guardrails, re-assembles the
// monolithic payload for cost-accounting/caching, and handles sudden client disconnections gracefully.
func (h *Handlers) serveStream(w http.ResponseWriter, r *http.Request, rt *Runtime, c *pipeline.Call, handled bool) {
	ctx := r.Context()
	defer func() { h.Pipeline.After(ctx, c) }()

	var chunks <-chan *api.ChatChunk
	var upstreamErr func() error
	var firstAt time.Time
	var stream provider.Stream
	var cancel context.CancelFunc

	if handled { // Cache Hit
		ch := make(chan *api.ChatChunk)
		go func() {
			defer close(ch)
			for _, x := range api.ChunksFromResponse(c.Response, 64) {
				x := x
				select {
				case ch <- &x:
				case <-ctx.Done():
					return
				}
			}
		}()
		chunks, upstreamErr = ch, func() error { return nil }
	} else { // Live Upstream Execution
		var first *api.ChatChunk
		var err error
		stream, first, cancel, err = h.openStream(ctx, rt, c)
		if err != nil {
			c.Err = upstreamError(err)
			if c.Err == nil { // client left
				c.Err = ctx.Err()
				return
			}
			writeCallHeaders(w, c)
			api.WriteError(w, c.Err)
			return
		}
		defer cancel()
		defer stream.Close()
		firstAt = time.Now()
		c.TTFT = time.Since(c.Start)
		chunks, upstreamErr = pump(ctx, stream, first, c.Route.StreamIdleTimeout, cancel)
	}

	sw, err := sse.NewWriter(w)
	if err != nil {
		c.Err = err
		api.WriteError(w, api.Internal())
		return
	}

	writeCallHeaders(w, c)
	w.Header().Set("Trailer", "X-ProofGate-Cost-USD")

	var chunkFilter func(*api.ChatChunk) *api.ChatChunk
	if fn, ok := c.Values["guard.chunk_filter"].(func(*api.ChatChunk) *api.ChatChunk); ok {
		chunkFilter = fn
	}

	var asm api.Assembler
	clientGone := false

	// Inner loop reading chunks from the pump and pushing them down the TCP socket via SSE.
	for ch := range chunks {
		asm.Add(ch) // Accumulate the monolithic result in memory for ledger/cache

		if usageOnly(ch) && !wantsUsage(c.Request) {
			continue
		}
		outCh := ch
		if chunkFilter != nil {
			outCh = chunkFilter(ch)
		}

		if err := sw.Data(outCh); err != nil {
			// The socket is broken (client aborted)
			clientGone = true
			break
		}
	}

	if flushFn, ok := c.Values["guard.stream_flush"].(func() string); ok {
		if rem := flushFn(); rem != "" && !clientGone {
			_ = sw.Data(&api.ChatChunk{
				Choices: []api.ChunkChoice{{Delta: api.ChunkDelta{Content: rem}}},
			})
		}
	}

	if clientGone {
		if cancel != nil {
			cancel()
		}
		if stream != nil {
			_ = stream.Close()
		}
	}

	if err := upstreamErr(); err != nil && !clientGone {
		_ = sw.Data(map[string]*api.Error{"error": api.Upstream("stream interrupted: " + err.Error())})
		c.Err = err
	}

	if !clientGone {
		_ = sw.Done() // Send standard "[DONE]" terminator
	} else {
		c.Err = context.Canceled
	}

	c.Response = asm.Response()
	c.Latency = time.Since(c.Start)

	if !handled {
		price(rt, c, asm.Text())
		h.observe(health.Sample{Target: c.Target, Tokens: c.Usage.CompletionTokens, Gen: time.Since(firstAt)})
	}
	w.Header().Set("X-ProofGate-Cost-USD", usd(c.CostMicros)) // sent as a trailer
}

// pump delegates reading from the upstream streaming connection into a dedicated goroutine.
// This is necessary because some provider SDKs execute blocking network reads that don't respect
// context cancellation promptly. By putting it in a goroutine, the gateway can enforce strict
// StreamIdleTimeouts (breaking the connection if the provider stalls mid-generation) without blocking
// the main handler thread.
func pump(ctx context.Context, s provider.Stream, first *api.ChatChunk, idle time.Duration, cancel context.CancelFunc) (<-chan *api.ChatChunk, func() error) {
	out := make(chan *api.ChatChunk, 16)
	var finalErr error
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer close(out)

		timer := time.AfterFunc(idle, cancel)
		defer timer.Stop()

		send := func(c *api.ChatChunk) bool {
			select {
			case out <- c:
				return true
			case <-ctx.Done(): // client disconnected
				return false
			}
		}

		if !send(first) {
			return
		}

		for {
			c, err := s.Recv()
			timer.Reset(idle) // Reset stall timer upon receiving valid data

			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				if ctx.Err() == nil {
					finalErr = err
				}
				return
			}
			if !send(c) {
				return
			}
		}
	}()
	return out, func() error { <-done; return finalErr }
}
