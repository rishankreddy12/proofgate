package mockllm

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/sse"
	"github.com/stretchr/testify/require"
)

func post(t *testing.T, url string, body any, hdr map[string]string) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func chatReq(stream bool, maxTokens int) api.ChatRequest {
	return api.ChatRequest{Model: "m", Stream: stream, MaxTokens: &maxTokens,
		StreamOptions: &api.StreamOptions{IncludeUsage: true},
		Messages:      []api.Message{{Role: "user", Content: api.Content{Text: "hello there"}}}}
}

func TestNonStream(t *testing.T) {
	srv := httptest.NewServer(New("a", Mode{}).Handler())
	defer srv.Close()
	resp := post(t, srv.URL+"/v1/chat/completions", chatReq(false, 5), nil)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	var out api.ChatResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, "echo: hello there lorem lorem", out.Choices[0].Message.Content.Text)
	require.Equal(t, "length", out.Choices[0].FinishReason)
	require.Equal(t, 2, out.Usage.PromptTokens)
	require.Equal(t, 5, out.Usage.CompletionTokens)
}

func TestStreamWithTTFTAndUsage(t *testing.T) {
	srv := httptest.NewServer(New("a", Mode{TTFTMs: 80, TokensPerSec: 1000}).Handler())
	defer srv.Close()
	start := time.Now()
	resp := post(t, srv.URL+"/v1/chat/completions", chatReq(true, 4), nil)
	defer resp.Body.Close()
	r := sse.NewReader(resp.Body)
	first, err := r.Next()
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), 80*time.Millisecond)
	var c api.ChatChunk
	require.NoError(t, json.Unmarshal(first.Data, &c))
	require.Equal(t, "assistant", c.Choices[0].Delta.Role)

	var a api.Assembler
	a.Add(&c)
	for {
		e, err := r.Next()
		if err == io.EOF || string(e.Data) == "[DONE]" {
			break
		}
		require.NoError(t, err)
		var ch api.ChatChunk
		require.NoError(t, json.Unmarshal(e.Data, &ch))
		a.Add(&ch)
	}
	require.Equal(t, "echo: hello there lorem", a.Text())
	require.Equal(t, 4, a.Response().Usage.CompletionTokens)
}

func TestErrorsAndAdminMode(t *testing.T) {
	s := New("a", Mode{})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp := post(t, srv.URL+"/v1/chat/completions", chatReq(false, 3), map[string]string{"X-Mock-Status": "503"})
	require.Equal(t, 503, resp.StatusCode)
	resp.Body.Close()

	b, _ := json.Marshal(Mode{ErrorRate: 1, ErrorStatus: 429})
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/admin/mode", bytes.NewReader(b))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	resp = post(t, srv.URL+"/v1/chat/completions", chatReq(false, 3), nil)
	require.Equal(t, 429, resp.StatusCode)
	resp.Body.Close()
	require.EqualValues(t, 2, s.Requests())
}

func cosine(a, b []float32) float64 {
	var d, na, nb float64
	for i := range a {
		d += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	return d / (math.Sqrt(na) * math.Sqrt(nb))
}

func TestHashEmbeddingSimilarity(t *testing.T) {
	a := HashEmbedding("how do I reset my password", 256)
	b := HashEmbedding("how can I reset my password", 256)
	c := HashEmbedding("best pizza in naples", 256)
	require.Len(t, a, 256)
	require.Greater(t, cosine(a, b), 0.7)
	require.Less(t, cosine(a, c), 0.3)
}
