package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/stretchr/testify/require"
)

func userReq(text string, max int) *api.ChatRequest {
	return &api.ChatRequest{Model: "ignored", MaxTokens: &max,
		Messages: []api.Message{{Role: "user", Content: api.Content{Text: text}}}}
}

func drain(t *testing.T, st Stream) *api.Assembler {
	t.Helper()
	var a api.Assembler
	for {
		c, err := st.Recv()
		if errors.Is(err, io.EOF) {
			return &a
		}
		require.NoError(t, err)
		a.Add(c)
	}
}

func TestOpenAIChatAgainstMock(t *testing.T) {
	srv := httptest.NewServer(mockllm.New("a", mockllm.Mode{}).Handler())
	defer srv.Close()
	p := NewOpenAI(OpenAIConfig{Name: "mock", BaseURL: srv.URL + "/v1"})
	resp, err := p.Chat(context.Background(), "mock-small", userReq("hi", 3))
	require.NoError(t, err)
	require.Equal(t, "mock-small", resp.Model)
	require.Equal(t, "echo: hi lorem", resp.Choices[0].Message.Content.Text)
}

func TestOpenAIStreamAlwaysRequestsUsage(t *testing.T) {
	srv := httptest.NewServer(mockllm.New("a", mockllm.Mode{}).Handler())
	defer srv.Close()
	p := NewOpenAI(OpenAIConfig{Name: "mock", BaseURL: srv.URL + "/v1"})
	req := userReq("hi", 3)
	st, err := p.ChatStream(context.Background(), "m", req)
	require.NoError(t, err)
	defer st.Close()
	a := drain(t, st)
	require.Equal(t, "echo: hi lorem", a.Text())
	require.Equal(t, 3, a.Response().Usage.CompletionTokens)
	require.Nil(t, req.StreamOptions, "the caller's request must not be mutated")
}

func TestOpenAIErrorAndAuthHeader(t *testing.T) {
	var gotAuth, gotExtra string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotExtra = r.Header.Get("Authorization"), r.Header.Get("X-Extra")
		w.WriteHeader(429)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "slow down"}})
	}))
	defer srv.Close()
	p := NewOpenAI(OpenAIConfig{Name: "o", BaseURL: srv.URL, APIKey: "sk-test", Headers: map[string]string{"X-Extra": "1"}})
	_, err := p.Chat(context.Background(), "m", userReq("x", 1))
	var pe *Error
	require.ErrorAs(t, err, &pe)
	require.Equal(t, 429, pe.Status)
	require.True(t, pe.Retryable)
	require.Equal(t, "Bearer sk-test", gotAuth)
	require.Equal(t, "1", gotExtra)
}

func TestOpenAIEmbed(t *testing.T) {
	srv := httptest.NewServer(mockllm.New("a", mockllm.Mode{}).Handler())
	defer srv.Close()
	p := NewOpenAI(OpenAIConfig{Name: "mock", BaseURL: srv.URL + "/v1"})
	out, err := p.Embed(context.Background(), "emb", &api.EmbeddingRequest{Input: api.StringOrSlice{"a b", "c"}})
	require.NoError(t, err)
	require.Len(t, out.Data, 2)
	require.Len(t, out.Data[0].Embedding, 256)
}
