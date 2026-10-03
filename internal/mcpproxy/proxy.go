// Package mcpproxy provides enterprise-grade capabilities, configuration, and structural components for the mcpproxy subsystem.
package mcpproxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/proofgate/proofgate/internal/agentrun"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/httpx"
	"github.com/proofgate/proofgate/internal/sse"
)

// Upstream defines the core enterprise configuration and state for Upstream.
// It is responsible for managing the lifecycle, validation, and schema of the Upstream entity.
type Upstream struct {
	Name, URL string
	Headers   map[string]string
}

// Audit defines the core enterprise configuration and state for Audit.
// It is responsible for managing the lifecycle, validation, and schema of the Audit entity.
type Audit struct {
	TS                                                     time.Time
	TenantID, KeyID, Server, Method, Tool, RunID, Decision string
	Status                                                 int
	LatencyMs                                              uint32
}

// Deps defines the core enterprise configuration and state for Deps.
// It is responsible for managing the lifecycle, validation, and schema of the Deps entity.
type Deps struct {
	Upstreams           func() map[string]Upstream
	Runs                agentrun.Store
	Audit               func(Audit) bool
	Client              *http.Client
	MaxRequestBodyBytes int64
	RequestTimeout      time.Duration
}

// Proxy defines the core enterprise configuration and state for Proxy.
// It is responsible for managing the lifecycle, validation, and schema of the Proxy entity.
type Proxy struct{ d Deps }

// New executes the primary logic for the New operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func New(d Deps) *Proxy {
	if d.Client == nil {
		d.Client = httpx.New() // no timeout: GET streams are long-lived; POST requests have explicit deadline
	}
	if d.Audit == nil {
		d.Audit = func(Audit) bool { return true }
	}
	return &Proxy{d: d}
}

var passHeaders = []string{"Content-Type", "Accept", "Mcp-Session-Id", "MCP-Protocol-Version", "Last-Event-ID"}

func (p *Proxy) forward(r *http.Request, up Upstream, body []byte) (*http.Response, error) {
	ctx := r.Context()
	if r.Method == http.MethodPost {
		timeout := p.d.RequestTimeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, up.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for _, h := range passHeaders {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	for k, v := range up.Headers { // configured credentials; the client's Authorization is never copied
		req.Header.Set(k, v)
	}
	return p.d.Client.Do(req)
}

// ServeHTTP executes the primary logic for the ServeHTTP operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("server")
	if name == "" {
		name = chi.URLParam(r, "server")
	}
	up, ok := p.d.Upstreams()[name]
	if !ok {
		api.WriteError(w, &api.Error{Status: 404, Message: "unknown MCP server " + name, Type: "invalid_request_error", Code: "invalid_request"})
		return
	}
	principal, _ := auth.FromContext(r.Context())
	runID := r.Header.Get("X-ProofGate-Run-Id")
	if runID == "" {
		sessionID := r.Header.Get("Mcp-Session-Id")
		sum := sha256.Sum256([]byte(principal.TenantID + "|" + principal.KeyID + "|" + sessionID))
		runID = fmt.Sprintf("mcp_%x", sum[:16])
	}
	start := time.Now()
	if r.Method != http.MethodPost {
		p.passthrough(w, r, up, nil, func(status int, filtered bool) {
			p.d.Audit(Audit{
				TS:        start,
				TenantID:  principal.TenantID,
				KeyID:     principal.KeyID,
				Server:    name,
				Method:    r.Method,
				RunID:     runID,
				Decision:  "allowed",
				Status:    status,
				LatencyMs: uint32(time.Since(start).Milliseconds()),
			})
		})
		return
	}
	maxBody := p.d.MaxRequestBodyBytes
	if maxBody <= 0 {
		maxBody = 4 << 20
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		api.WriteError(w, api.BadRequest("body too large or unreadable"))
		return
	}
	msgs, batch, err := Parse(body)
	if err != nil {
		api.WriteError(w, api.BadRequest("invalid JSON-RPC"))
		return
	}

	var refused []Message
	listIDs := map[string]bool{}
	for _, m := range msgs {
		if !MethodAllowed(principal.Key.MCP, name, m.Method) {
			refused = append(refused, ErrorMessage(m.ID, CodeMethodDenied, fmt.Sprintf("method %s is not allowed for this key on server %s", m.Method, name)))
			p.d.Audit(Audit{
				TS:        start,
				TenantID:  principal.TenantID,
				KeyID:     principal.KeyID,
				Server:    name,
				Method:    m.Method,
				RunID:     runID,
				Decision:  "denied",
				Status:    http.StatusOK,
				LatencyMs: uint32(time.Since(start).Milliseconds()),
			})
			continue
		}
		if m.Method == "tools/list" {
			listIDs[string(m.ID)] = true
		}
		tool, args, isCall := ToolCall(m)
		if isCall {
			if !Allowed(principal.Key.MCP, name, tool) {
				refused = append(refused, ErrorMessage(m.ID, CodeToolDenied, fmt.Sprintf("tool %s is not allowed for this key on server %s", tool, name)))
				p.d.Audit(Audit{
					TS:        start,
					TenantID:  principal.TenantID,
					KeyID:     principal.KeyID,
					Server:    name,
					Method:    m.Method,
					Tool:      tool,
					RunID:     runID,
					Decision:  "denied",
					Status:    http.StatusOK,
					LatencyMs: uint32(time.Since(start).Milliseconds()),
				})
				continue
			}
			if rp := principal.Key.Run; rp != nil && runID != "" && p.d.Runs != nil && agentrun.RunIDPattern.MatchString(runID) {
				fp := "mcp:" + name + ":" + tool + ":" + agentrun.Normalize(string(args))
				res, err := p.d.Runs.Step(r.Context(), principal.TenantID, runID, rp.WithDefaults(), fp, 0, 0)
				if err == nil && res.Status != agentrun.Allowed {
					refused = append(refused, ErrorMessage(m.ID, CodeRunLimit, "run limit reached or loop detected for run "+runID))
					p.d.Audit(Audit{
						TS:        start,
						TenantID:  principal.TenantID,
						KeyID:     principal.KeyID,
						Server:    name,
						Method:    m.Method,
						Tool:      tool,
						RunID:     runID,
						Decision:  "run_limit",
						Status:    http.StatusOK,
						LatencyMs: uint32(time.Since(start).Milliseconds()),
					})
					continue
				}
			}
		}
	}
	if len(refused) > 0 {
		writeRefusal(w, msgs, refused, batch)
		return
	}

	filter := &filterSpec{
		body:    body,
		listIDs: listIDs,
		allow:   func(t string) bool { return Allowed(principal.Key.MCP, name, t) },
	}
	p.passthrough(w, r, up, filter, func(status int, filtered bool) {
		latencyMs := uint32(time.Since(start).Milliseconds())
		for _, m := range msgs {
			tool, _, _ := ToolCall(m)
			dec := "allowed"
			if m.Method == "tools/list" && filtered {
				dec = "filtered"
			}
			p.d.Audit(Audit{
				TS:        start,
				TenantID:  principal.TenantID,
				KeyID:     principal.KeyID,
				Server:    name,
				Method:    m.Method,
				Tool:      tool,
				RunID:     runID,
				Decision:  dec,
				Status:    status,
				LatencyMs: latencyMs,
			})
		}
	})
}

func writeRefusal(w http.ResponseWriter, msgs, refused []Message, batch bool) {
	w.Header().Set("Content-Type", "application/json")
	if !batch {
		_ = json.NewEncoder(w).Encode(refused[0])
		return
	}
	done := map[string]bool{}
	for _, m := range refused {
		done[string(m.ID)] = true
	}
	out := refused
	for _, m := range msgs {
		if len(m.ID) > 0 && !done[string(m.ID)] {
			out = append(out, ErrorMessage(m.ID, CodeRunLimit, "batch not forwarded because another call in it was refused"))
		}
	}
	_ = json.NewEncoder(w).Encode(out)
}

type filterSpec struct {
	body    []byte
	listIDs map[string]bool
	allow   func(string) bool
}

func (f *filterSpec) apply(data []byte) ([]byte, bool) {
	if f == nil || len(f.listIDs) == 0 {
		return data, false
	}
	msgs, batch, err := Parse(data)
	if err != nil {
		return data, false
	}
	changed := false
	anyFiltered := false
	for i, m := range msgs {
		if f.listIDs[string(m.ID)] && len(m.Result) > 0 {
			if out, didFilter, err := FilterToolsList(m.Result, f.allow); err == nil {
				msgs[i].Result, changed = out, true
				if didFilter {
					anyFiltered = true
				}
			}
		}
	}
	if !changed {
		return data, false
	}
	var b []byte
	if batch {
		b, _ = json.Marshal(msgs)
	} else {
		b, _ = json.Marshal(msgs[0])
	}
	return b, anyFiltered
}

func (p *Proxy) passthrough(w http.ResponseWriter, r *http.Request, up Upstream, f *filterSpec, onComplete func(status int, filtered bool)) {
	var body []byte
	if f != nil {
		body = f.body
	}
	resp, err := p.forward(r, up, body)
	if err != nil {
		slog.Warn("mcp upstream failed", "server", up.Name, "err", err)
		if onComplete != nil {
			onComplete(http.StatusBadGateway, false)
		}
		api.WriteError(w, api.Upstream("MCP server unavailable"))
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Mcp-Session-Id"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		sw, err := sse.NewWriter(w)
		if err != nil {
			if onComplete != nil {
				onComplete(resp.StatusCode, false)
			}
			return
		}
		rd := sse.NewReader(resp.Body)
		anyFiltered := false
		for {
			ev, err := rd.Next()
			if err != nil {
				break
			}
			var didFilter bool
			ev.Data, didFilter = f.apply(ev.Data)
			if didFilter {
				anyFiltered = true
			}
			if sw.Event(ev) != nil {
				break
			}
		}
		if onComplete != nil {
			onComplete(resp.StatusCode, anyFiltered)
		}
		return
	}
	w.WriteHeader(resp.StatusCode)
	if f != nil && len(f.listIDs) > 0 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		out, didFilter := f.apply(b)
		_, _ = w.Write(out)
		if onComplete != nil {
			onComplete(resp.StatusCode, didFilter)
		}
		return
	}
	_, _ = io.Copy(w, resp.Body)
	if onComplete != nil {
		onComplete(resp.StatusCode, false)
	}
}
