package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/provider"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/proofgate/proofgate/internal/sse"
)

func wantsUsage(req *api.ChatRequest) bool {
	return req.StreamOptions != nil && req.StreamOptions.IncludeUsage
}

func usageOnly(c *api.ChatChunk) bool { return c.Usage != nil && len(c.Choices) == 0 }

// openStream runs the router until one target produces a first chunk (rule 1 in the plan).
func (h *Handlers) openStream(ctx context.Context, rt *Runtime, c *pipeline.Call) (provider.Stream, *api.ChatChunk, context.CancelFunc, error) {
	var (
		stream provider.Stream
		first  *api.ChatChunk
		cancel context.CancelFunc
	)
	res, err := router.Execute(ctx, rt.Router.Plan(c.Route), c.Route.Retry, h.Breakers,
		func(ctx context.Context, t router.Target) error {
			p, ok := rt.Registry.Get(t.Provider)
			if !ok {
				return api.NoHealthyTarget()
			}
			sctx, scancel := context.WithCancel(ctx)
			ttft := time.AfterFunc(c.Route.Timeout, scancel)
			start := time.Now()
			s, err := p.ChatStream(sctx, t.Model, c.Request)
			if err == nil {
				first, err = s.Recv()
				if err != nil {
					s.Close()
				}
			}
			stoppedInTime := ttft.Stop()
			c.UpstreamTime += time.Since(start)
			if err != nil {
				scancel()
				if !stoppedInTime && ctx.Err() == nil {
					return &provider.Error{Provider: t.Provider, Status: 504, Message: "time to first token exceeded", Retryable: true}
				}
				if errors.Is(err, io.EOF) {
					return &provider.Error{Provider: t.Provider, Status: 502, Message: "empty stream", Retryable: true}
				}
				return err
			}
			stream, cancel = s, scancel
			return nil
		})
	c.Target, c.Attempts = res.Target, res.Attempts
	return stream, first, cancel, err
}

func (h *Handlers) serveStream(w http.ResponseWriter, r *http.Request, rt *Runtime, c *pipeline.Call, handled bool) {
	ctx := r.Context()
	defer func() { h.Pipeline.After(ctx, c) }()

	var chunks <-chan *api.ChatChunk
	var upstreamErr func() error
	if handled {
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
	} else {
		stream, first, cancel, err := h.openStream(ctx, rt, c)
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
	var asm api.Assembler
	clientGone := false
	for ch := range chunks {
		asm.Add(ch)
		if usageOnly(ch) && !wantsUsage(c.Request) {
			continue
		}
		if err := sw.Data(ch); err != nil {
			clientGone = true
			break
		}
	}
	if err := upstreamErr(); err != nil && !clientGone {
		_ = sw.Data(map[string]*api.Error{"error": api.Upstream("stream interrupted: " + err.Error())})
		c.Err = err
	}
	if !clientGone {
		_ = sw.Done()
	} else {
		c.Err = context.Canceled
	}
	c.Response = asm.Response()
	c.Latency = time.Since(c.Start)
	if !handled {
		price(rt, c, asm.Text())
	}
	w.Header().Set("X-ProofGate-Cost-USD", usd(c.CostMicros)) // sent as a trailer
}

// pump reads the upstream stream in its own goroutine so the idle timeout can cancel a blocked Recv.
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
			case <-ctx.Done():
				return false
			}
		}
		if !send(first) {
			return
		}
		for {
			c, err := s.Recv()
			timer.Reset(idle)
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
