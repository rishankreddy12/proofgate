package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/stretchr/testify/require"
)

type env struct {
	gw    *httptest.Server
	mocks []*mockllm.Server
	h     *Handlers
}

// setup starts mock-a and mock-b with the given modes and a gateway in front of them.
// Routes: default = [a/small, b/large]; fast = same targets with a 100ms timeout; embed = a/emb.
func setup(t *testing.T, a, b mockllm.Mode, stages ...pipeline.Stage) *env {
	t.Helper()
	e := &env{}
	var urls []string
	for i, m := range []mockllm.Mode{a, b} {
		s := mockllm.New(fmt.Sprintf("m%d", i), m)
		srv := httptest.NewServer(s.Handler())
		t.Cleanup(srv.Close)
		e.mocks = append(e.mocks, s)
		urls = append(urls, srv.URL+"/v1")
	}
	cfg, err := config.Parse([]byte(fmt.Sprintf(`
providers:
  - {name: a, type: openai, base_url: %q}
  - {name: b, type: openai, base_url: %q}
pricing:
  a/small: {input: 1, output: 2}
  b/large: {input: 10, output: 20}
  a/emb:   {input: 0.5, output: 0}
routes:
  - name: default
    retry: {max_attempts: 2, base_delay: 1ms}
    targets: [{provider: a, model: small}, {provider: b, model: large}]
  - name: fast
    timeout: 100ms
    retry: {max_attempts: 1, base_delay: 1ms}
    targets: [{provider: a, model: small}, {provider: b, model: large}]
  - name: embed
    embeddings: true
    targets: [{provider: a, model: emb}]
`, urls[0], urls[1])))
	require.NoError(t, err)
	br := router.NewBreakers(5, time.Minute, time.Now)
	rt, err := BuildRuntime(cfg, br, os.Getenv)
	require.NoError(t, err)
	st := &State{}
	st.Store(rt)
	e.h = &Handlers{State: st, Breakers: br, Pipeline: pipeline.New(stages...), Now: time.Now}
	principal := auth.Principal{TenantID: "t1", TenantName: "acme", AllowedRoutes: []string{"default", "fast", "embed"}}
	fakeAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}
	e.gw = httptest.NewServer(e.h.Routes(fakeAuth))
	t.Cleanup(e.gw.Close)
	return e
}

func (e *env) post(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(e.gw.URL+path, "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	return resp
}

func chat(model string, stream bool, text string) api.ChatRequest {
	max := 4
	return api.ChatRequest{Model: model, Stream: stream, MaxTokens: &max,
		Messages: []api.Message{{Role: "user", Content: api.Content{Text: text}}}}
}
