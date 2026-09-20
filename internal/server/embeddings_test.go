package server

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/stretchr/testify/require"
)

func TestEmbeddings(t *testing.T) {
	e := setup(t, mockllm.Mode{}, mockllm.Mode{})
	resp := e.post(t, "/v1/embeddings", api.EmbeddingRequest{Model: "embed", Input: api.StringOrSlice{"a b c", "d"}})
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	var out api.EmbeddingResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Len(t, out.Data, 2)
	require.Equal(t, "a/emb", resp.Header.Get("X-ProofGate-Target"))
	// 4 tokens * $0.5/M = 2 micro-USD
	require.Equal(t, "0.000002", resp.Header.Get("X-ProofGate-Cost-USD"))

	resp2 := e.post(t, "/v1/embeddings", api.EmbeddingRequest{Model: "default", Input: api.StringOrSlice{"x"}})
	b, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	require.Equal(t, 400, resp2.StatusCode, string(b))
}

func TestModels(t *testing.T) {
	e := setup(t, mockllm.Mode{}, mockllm.Mode{})
	resp, err := http.Get(e.gw.URL + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	var ids []string
	for _, d := range out.Data {
		ids = append(ids, d.ID)
	}
	require.Equal(t, []string{"default", "fast", "embed"}, ids)
}
