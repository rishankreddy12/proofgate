package mcpproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/proofgate/proofgate/internal/agentrun"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/sse"
)

type Upstream struct {
	Name, URL string
	Headers   map[string]string
}

type Audit struct {
	TS                                                   time.Time
	TenantID, KeyID, Server, Method, Tool, RunID, Decision string
	Status                                               int
	LatencyMs                                            uint32
}

type Deps struct {
	Upstreams func() map[string]Upstream
	Runs      agentrun.Store
	Audit     func(Audit) bool
	Client    *http.Client
}

type Proxy struct{ d Deps }

func New(d Deps) *Proxy {
	if d.Client == nil {
		d.Client = &http.Client{} // no timeout: GET streams are long-lived
	}
	if d.Audit == nil {
		d.Audit = func(Audit) bool { return true }
	}
	return &Proxy{d: d}
}

var passHeaders = []string{"Content-Type", "Accept", "Mcp-Session-Id", "MCP-Protocol-Version", "Last-Event-ID"}

func (p *Proxy) forward(r *http.Request, up Upstream, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, up.URL, bytes.NewReader(body))
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

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("server")
	up, ok := p.d.Upstreams()[name]
	if !ok {
		api.WriteError(w, &api.Error{Status: 404, Message: "unknown MCP server " + name, Type: "invalid_request_error", Code: "invalid_request"})
		return
	}
	principal, _ := auth.FromContext(r.Context())
	if r.Method != http.MethodPost {
		p.passthrough(w, r, up, nil)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		api.WriteError(w, api.BadRequest("body too large or unreadable"))
		return
	}
	msgs, batch, err := Parse(body)
	if err != nil {
		api.WriteError(w, api.BadRequest("invalid JSON-RPC"))
		return
	}
	runID := r.Header.Get("X-ProofGate-Run-Id")
	if runID == "" {
		runID = r.Header.Get("Mcp-Session-Id")
	}
	audit := func(m Message, tool, decision string, status int, start time.Time) {
		p.d.Audit(Audit{TS: start, TenantID: principal.TenantID, KeyID: principal.KeyID, Server: name, Method: m.Method,
			Tool: tool, RunID: runID, Decision: decision, Status: status, LatencyMs: uint32(time.Since(start).Milliseconds())})
	}
	start := time.Now()
	var refused []Message
	listIDs := map[string]bool{}
	for _, m := range msgs {
		if m.Method == "tools/list" {
			listIDs[string(m.ID)] = true
		}
		tool, args, isCall := ToolCall(m)
		if !isCall {
			continue
		}
		if !Allowed(principal.Key.MCP, name, tool) {
			refused = append(refused, ErrorMessage(m.ID, CodeToolDenied, fmt.Sprintf("tool %s is not allowed for this key on server %s", tool, name)))
			audit(m, tool, "denied", 200, start)
			continue
		}
		if rp := principal.Key.Run; rp != nil && runID != "" && p.d.Runs != nil && agentrun.RunIDPattern.MatchString(runID) {
			fp := "mcp:" + name + ":" + tool + ":" + agentrun.Normalize(string(args))
			res, err := p.d.Runs.Step(r.Context(), principal.TenantID, runID, rp.WithDefaults(), fp)
			if err == nil && res.Status != agentrun.Allowed {
				refused = append(refused, ErrorMessage(m.ID, CodeRunLimit, "run limit reached or loop detected for run "+runID))
				audit(m, tool, "run_limit", 200, start)
				continue
			}
		}
		audit(m, tool, "allowed", 200, start)
	}
	if len(refused) > 0 {
		writeRefusal(w, msgs, refused, batch)
		return
	}
	p.passthrough(w, r, up, &filterSpec{body: body, listIDs: listIDs, allow: func(t string) bool { return Allowed(principal.Key.MCP, name, t) }})
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

func (f *filterSpec) apply(data []byte) []byte {
	if f == nil || len(f.listIDs) == 0 {
		return data
	}
	msgs, batch, err := Parse(data)
	if err != nil {
		return data
	}
	changed := false
	for i, m := range msgs {
		if f.listIDs[string(m.ID)] && len(m.Result) > 0 {
			if out, err := FilterToolsList(m.Result, f.allow); err == nil {
				msgs[i].Result, changed = out, true
			}
		}
	}
	if !changed {
		return data
	}
	var b []byte
	if batch {
		b, _ = json.Marshal(msgs)
	} else {
		b, _ = json.Marshal(msgs[0])
	}
	return b
}

func (p *Proxy) passthrough(w http.ResponseWriter, r *http.Request, up Upstream, f *filterSpec) {
	var body []byte
	if f != nil {
		body = f.body
	}
	resp, err := p.forward(r, up, body)
	if err != nil {
		slog.Warn("mcp upstream failed", "server", up.Name, "err", err)
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
			return
		}
		rd := sse.NewReader(resp.Body)
		for {
			ev, err := rd.Next()
			if err != nil {
				return
			}
			ev.Data = f.apply(ev.Data)
			if sw.Event(ev) != nil {
				return
			}
		}
	}
	w.WriteHeader(resp.StatusCode)
	if f != nil && len(f.listIDs) > 0 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		_, _ = w.Write(f.apply(b))
		return
	}
	_, _ = io.Copy(w, resp.Body)
}
