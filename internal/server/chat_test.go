package server

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/stretchr/testify/require"
)

func TestChatSuccessHeadersAndCost(t *testing.T) {
	e := setup(t, mockllm.Mode{}, mockllm.Mode{})
	resp := e.post(t, "/v1/chat/completions", chat("default", false, "hi"))
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	var out api.ChatResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, "echo: hi lorem lorem", out.Choices[0].Message.Content.Text)
	require.Equal(t, "a/small", resp.Header.Get("X-ProofGate-Target"))
	require.Equal(t, "default", resp.Header.Get("X-ProofGate-Route"))
	require.Equal(t, "1", resp.Header.Get("X-ProofGate-Attempts"))
	require.Equal(t, "miss", resp.Header.Get("X-ProofGate-Cache"))
	require.NotEmpty(t, resp.Header.Get("X-ProofGate-Request-Id"))
	// usage: prompt 1 word, completion 4 words: 1*1 + 4*2 = 9 micro-USD
	require.Equal(t, "0.000009", resp.Header.Get("X-ProofGate-Cost-USD"))
}

func TestChatFailsOverOn503(t *testing.T) {
	e := setup(t, mockllm.Mode{ErrorRate: 1, ErrorStatus: 503}, mockllm.Mode{})
	resp := e.post(t, "/v1/chat/completions", chat("default", false, "hi"))
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "b/large", resp.Header.Get("X-ProofGate-Target"))
	require.Equal(t, "3", resp.Header.Get("X-ProofGate-Attempts"))
	require.EqualValues(t, 2, e.mocks[0].Requests())
}

func TestChatUpstreamBadRequestIsNotRetried(t *testing.T) {
	e := setup(t, mockllm.Mode{ErrorRate: 1, ErrorStatus: 400}, mockllm.Mode{})
	resp := e.post(t, "/v1/chat/completions", chat("default", false, "hi"))
	defer resp.Body.Close()
	require.Equal(t, 400, resp.StatusCode)
	b, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(b), "upstream_rejected")
	require.EqualValues(t, 0, e.mocks[1].Requests())
}

func TestChatValidation(t *testing.T) {
	e := setup(t, mockllm.Mode{}, mockllm.Mode{})
	cases := []struct {
		body   any
		status int
		code   string
	}{
		{map[string]any{"model": "default"}, 400, "invalid_request"},
		{chat("", false, "x"), 400, "invalid_request"},
		{chat("nope", false, "x"), 400, "invalid_request"},
		{chat("a/small", false, "x"), 403, "route_forbidden"},
		{chat("embed", false, "x"), 400, "invalid_request"},
	}
	two := 2
	nReq := chat("default", false, "x")
	nReq.N = &two
	cases = append(cases, struct {
		body   any
		status int
		code   string
	}{nReq, 400, "invalid_request"})
	for _, c := range cases {
		resp := e.post(t, "/v1/chat/completions", c.body)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, c.status, resp.StatusCode, string(b))
		require.Contains(t, string(b), c.code)
	}
}

func TestAllTargetsDownIs502(t *testing.T) {
	e := setup(t, mockllm.Mode{ErrorRate: 1, ErrorStatus: 500}, mockllm.Mode{ErrorRate: 1, ErrorStatus: 500})
	resp := e.post(t, "/v1/chat/completions", chat("default", false, "hi"))
	defer resp.Body.Close()
	require.Equal(t, 502, resp.StatusCode)
}
