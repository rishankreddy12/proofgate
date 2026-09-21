package mcpproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/proofgate/proofgate/internal/agentrun"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/sse"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

const toolsList = `{"tools":[{"name":"get_file"},{"name":"write_file"},{"name":"delete_repo"}]}`

// fakeMCP answers tools/list and tools/call; it replies with SSE when the client prefers it.
func fakeMCP(t *testing.T, calls *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer upstream-token", r.Header.Get("Authorization"), "gateway key must never be forwarded")
		calls.Add(1)
		var m Message
		_ = json.NewDecoder(r.Body).Decode(&m)
		result := json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
		if m.Method == "tools/list" {
			result = json.RawMessage(toolsList)
		}
		resp, _ := json.Marshal(Message{JSONRPC: "2.0", ID: m.ID, Result: result})
		w.Header().Set("Mcp-Session-Id", "sess-1")
		if r.Header.Get("Accept") == "text/event-stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "id: 1\ndata: %s\n\n", resp)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
}

type nopRuns struct{ status agentrun.StepStatus }

func (n nopRuns) Step(context.Context, string, string, store.RunPolicy, string) (agentrun.StepResult, error) {
	return agentrun.StepResult{Status: n.status}, nil
}
func (nopRuns) Charge(context.Context, string, string, store.RunPolicy, int64, int) error { return nil }

func proxyServer(t *testing.T, upstream string, runs agentrun.Store, audits *[]Audit) *httptest.Server {
	p := New(Deps{
		Upstreams: func() map[string]Upstream {
			return map[string]Upstream{"github": {Name: "github", URL: upstream, Headers: map[string]string{"Authorization": "Bearer upstream-token"}}}
		},
		Runs:  runs,
		Audit: func(a Audit) bool { *audits = append(*audits, a); return true },
	})
	principal := auth.Principal{TenantID: "t", Key: store.KeyPolicy{
		MCP: &store.MCPPolicy{Servers: map[string]store.MCPServerPolicy{"github": {Allow: []string{"get_*", "write_*"}, Deny: []string{"write_*"}}}},
		Run: &store.RunPolicy{MaxSteps: 10},
	}}
	mux := http.NewServeMux()
	mux.Handle("/mcp/{server}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
	}))
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func rpc(t *testing.T, url, body, accept string) (*http.Response, []byte) {
	req, _ := http.NewRequest("POST", url, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer pg_live_gatewaykey")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	req.Header.Set("X-ProofGate-Run-Id", "run-1")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, b
}

func TestToolsListIsFilteredJSONAndSSE(t *testing.T) {
	var calls atomic.Int32
	up := fakeMCP(t, &calls)
	defer up.Close()
	var audits []Audit
	s := proxyServer(t, up.URL, nopRuns{status: agentrun.Allowed}, &audits)

	resp, b := rpc(t, s.URL+"/mcp/github", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "application/json")
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "sess-1", resp.Header.Get("Mcp-Session-Id"))
	require.Contains(t, string(b), `"get_file"`)
	require.NotContains(t, string(b), "write_file")
	require.NotContains(t, string(b), "delete_repo")

	resp, b = rpc(t, s.URL+"/mcp/github", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "text/event-stream")
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	ev, err := sse.NewReader(bytes.NewReader(b)).Next()
	require.NoError(t, err)
	require.Equal(t, "1", ev.ID)
	require.NotContains(t, string(ev.Data), "delete_repo")
}

func TestDeniedCallNeverReachesServer(t *testing.T) {
	var calls atomic.Int32
	up := fakeMCP(t, &calls)
	defer up.Close()
	var audits []Audit
	s := proxyServer(t, up.URL, nopRuns{status: agentrun.Allowed}, &audits)

	_, b := rpc(t, s.URL+"/mcp/github", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"delete_repo","arguments":{}}}`, "application/json")
	require.JSONEq(t, `{"jsonrpc":"2.0","id":3,"error":{"code":-32001,"message":"tool delete_repo is not allowed for this key on server github"}}`, string(b))
	require.EqualValues(t, 0, calls.Load())
	require.Equal(t, "denied", audits[0].Decision)
	require.Equal(t, "delete_repo", audits[0].Tool)

	_, b = rpc(t, s.URL+"/mcp/github", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_file","arguments":{"path":"x"}}}`, "application/json")
	require.Contains(t, string(b), `"ok"`)
	require.EqualValues(t, 1, calls.Load())
}

func TestRunLimitBlocksToolCall(t *testing.T) {
	var calls atomic.Int32
	up := fakeMCP(t, &calls)
	defer up.Close()
	var audits []Audit
	s := proxyServer(t, up.URL, nopRuns{status: agentrun.Loop}, &audits)
	_, b := rpc(t, s.URL+"/mcp/github", `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_file","arguments":{}}}`, "application/json")
	require.Contains(t, string(b), `-32002`)
	require.EqualValues(t, 0, calls.Load())
}

func TestUnknownServer(t *testing.T) {
	var audits []Audit
	s := proxyServer(t, "http://127.0.0.1:1", nopRuns{}, &audits)
	resp, _ := rpc(t, s.URL+"/mcp/slack", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "application/json")
	require.Equal(t, 404, resp.StatusCode)
}
