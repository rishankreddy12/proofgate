package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/proofgate/proofgate/internal/sse"
	"github.com/stretchr/testify/require"
)

func readStream(t *testing.T, resp *http.Response) (*api.Assembler, []api.ChatChunk, time.Duration) {
	t.Helper()
	start := time.Now()
	var first time.Duration
	var a api.Assembler
	var chunks []api.ChatChunk
	r := sse.NewReader(resp.Body)
	for {
		ev, err := r.Next()
		if err == io.EOF || string(ev.Data) == "[DONE]" {
			break
		}
		require.NoError(t, err)
		if first == 0 {
			first = time.Since(start)
		}
		var c api.ChatChunk
		require.NoError(t, json.Unmarshal(ev.Data, &c))
		chunks = append(chunks, c)
		a.Add(&c)
	}
	return &a, chunks, first
}

func TestStreamIsNotBuffered(t *testing.T) {
	// 4 tokens at 10 tokens/s: the full reply takes ~400ms; the first chunk must arrive long before.
	e := setup(t, mockllm.Mode{TokensPerSec: 10}, mockllm.Mode{})
	start := time.Now()
	resp := e.post(t, "/v1/chat/completions", chat("default", true, "hi"))
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	a, chunks, _ := readStream(t, resp)
	total := time.Since(start)
	require.GreaterOrEqual(t, total, 300*time.Millisecond)
	require.Equal(t, "echo: hi lorem lorem", a.Text())
	for _, c := range chunks {
		require.Nil(t, c.Usage, "client did not ask for usage")
	}
	_, _ = io.Copy(io.Discard, resp.Body) // trailers arrive after the body is fully read
	require.Equal(t, "0.000009", resp.Trailer.Get("X-ProofGate-Cost-USD"))
}

func TestStreamFirstChunkArrivesEarly(t *testing.T) {
	e := setup(t, mockllm.Mode{TokensPerSec: 5}, mockllm.Mode{})
	req := chat("default", true, "hi")
	start := time.Now() // before the request: headers are only sent together with the first chunk
	resp := e.post(t, "/v1/chat/completions", req)
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(line, "data: "))
	require.Less(t, time.Since(start), 150*time.Millisecond, "first chunk must not wait for the whole reply (800ms)")
}

func TestStreamIncludesUsageWhenAsked(t *testing.T) {
	e := setup(t, mockllm.Mode{}, mockllm.Mode{})
	req := chat("default", true, "hi")
	req.StreamOptions = &api.StreamOptions{IncludeUsage: true}
	resp := e.post(t, "/v1/chat/completions", req)
	defer resp.Body.Close()
	a, _, _ := readStream(t, resp)
	require.Equal(t, 4, a.Response().Usage.CompletionTokens)
}

func TestStreamFailsOverBeforeFirstByte(t *testing.T) {
	e := setup(t, mockllm.Mode{ErrorRate: 1, ErrorStatus: 503}, mockllm.Mode{})
	resp := e.post(t, "/v1/chat/completions", chat("default", true, "hi"))
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "b/large", resp.Header.Get("X-ProofGate-Target"))
	a, _, _ := readStream(t, resp)
	require.Equal(t, "echo: hi lorem lorem", a.Text())
}

func TestStreamTTFTTimeoutFailsOver(t *testing.T) {
	e := setup(t, mockllm.Mode{TTFTMs: 1000}, mockllm.Mode{})
	start := time.Now()
	resp := e.post(t, "/v1/chat/completions", chat("fast", true, "hi")) // fast: 100ms timeout, 1 attempt
	defer resp.Body.Close()
	require.Equal(t, "b/large", resp.Header.Get("X-ProofGate-Target"))
	readStream(t, resp)
	require.Less(t, time.Since(start), 600*time.Millisecond)
}

func TestStreamAllFailReturnsJSONError(t *testing.T) {
	e := setup(t, mockllm.Mode{ErrorRate: 1, ErrorStatus: 500}, mockllm.Mode{ErrorRate: 1, ErrorStatus: 500})
	resp := e.post(t, "/v1/chat/completions", chat("default", true, "hi"))
	defer resp.Body.Close()
	require.Equal(t, 502, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}

func TestClientDisconnectCancelsUpstream(t *testing.T) {
	upstreamDone := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; ; i++ {
			_, err := fmt.Fprintf(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"t%d \"}}]}\n\n", i)
			if err != nil {
				break
			}
			fl.Flush()
			select {
			case <-r.Context().Done():
				close(upstreamDone)
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}))
	defer up.Close()
	cfg, err := config.Parse([]byte(fmt.Sprintf(`
providers: [{name: u, type: openai, base_url: %q}]
routes: [{name: default, targets: [{provider: u, model: m}]}]`, up.URL)))
	require.NoError(t, err)
	br := router.NewBreakers(5, time.Minute, time.Now)
	rt, _ := BuildRuntime(cfg, br, os.Getenv)
	st := &State{}
	st.Store(rt)
	h := &Handlers{State: st, Breakers: br, Pipeline: pipeline.New(), Now: time.Now}
	gw := httptest.NewServer(h.Routes(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{TenantID: "t"})))
		})
	}))
	defer gw.Close()

	e := &env{gw: gw}
	resp := e.post(t, "/v1/chat/completions", chat("default", true, "hi"))
	br2 := bufio.NewReader(resp.Body)
	_, err = br2.ReadString('\n')
	require.NoError(t, err)
	resp.Body.Close() // client goes away
	select {
	case <-upstreamDone:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request was not cancelled after client disconnect")
	}
}
