package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/stretchr/testify/require"
)

func TestToGeminiTranslation(t *testing.T) {
	max := 64
	req := &api.ChatRequest{
		MaxTokens: &max,
		Messages: []api.Message{
			{Role: "system", Content: api.Content{Text: "be brief"}},
			{Role: "user", Content: api.Content{Text: "weather?"}},
			{Role: "assistant", ToolCalls: []api.ToolCall{{ID: "c1", Function: api.FunctionCall{Name: "wx", Arguments: `{"city":"Pune"}`}}}},
			{Role: "tool", ToolCallID: "c1", Content: api.Content{Text: "31C"}},
		},
		Tools: []api.Tool{{Type: "function", Function: api.FunctionDef{Name: "wx",
			Parameters: json.RawMessage(`{"$schema":"x","type":"object","additionalProperties":false,"properties":{"city":{"type":"string"}}}`)}}},
	}
	out, err := toGemini(req)
	require.NoError(t, err)
	b, _ := json.Marshal(out)
	require.JSONEq(t, `{
	  "systemInstruction":{"parts":[{"text":"be brief"}]},
	  "contents":[
	    {"role":"user","parts":[{"text":"weather?"}]},
	    {"role":"model","parts":[{"functionCall":{"name":"wx","args":{"city":"Pune"}}}]},
	    {"role":"user","parts":[{"functionResponse":{"name":"wx","response":{"content":"31C"}}}]}
	  ],
	  "generationConfig":{"maxOutputTokens":64},
	  "tools":[{"functionDeclarations":[{"name":"wx","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}]}]
	}`, string(b))
}

func TestGeminiChatAndStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "gk", r.Header.Get("x-goog-api-key"))
		if strings.HasSuffix(r.URL.Path, ":streamGenerateContent") {
			require.Equal(t, "sse", r.URL.Query().Get("alt"))
			_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hel"}]}}]}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2}}`+"\n\n")
			return
		}
		require.True(t, strings.HasSuffix(r.URL.Path, "/v1beta/models/gem-x:generateContent"))
		fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"},{"functionCall":{"name":"wx","args":{"city":"Pune"}}}]},"finishReason":"STOP"}],
		  "usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"cachedContentTokenCount":4}}`)
	}))
	defer srv.Close()
	p := NewGemini(GeminiConfig{Name: "gemini", BaseURL: srv.URL, APIKey: "gk"})

	resp, err := p.Chat(context.Background(), "gem-x", userReq("hi", 10))
	require.NoError(t, err)
	require.Equal(t, "ok", resp.Choices[0].Message.Content.Text)
	require.Equal(t, "tool_calls", resp.Choices[0].FinishReason)
	require.Equal(t, "call_0", resp.Choices[0].Message.ToolCalls[0].ID)
	require.Equal(t, `{"city":"Pune"}`, resp.Choices[0].Message.ToolCalls[0].Function.Arguments)
	require.Equal(t, 4, resp.Usage.CachedTokens())
	require.Equal(t, 13, resp.Usage.TotalTokens)

	st, err := p.ChatStream(context.Background(), "gem-x", userReq("hi", 10))
	require.NoError(t, err)
	r := drain(t, st).Response()
	require.Equal(t, "Hello", r.Choices[0].Message.Content.Text)
	require.Equal(t, "stop", r.Choices[0].FinishReason)
	require.Equal(t, 6, r.Usage.TotalTokens)
}

func TestGeminiEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasSuffix(r.URL.Path, "/v1beta/models/emb:batchEmbedContents"))
		fmt.Fprint(w, `{"embeddings":[{"values":[0.1,0.2]},{"values":[0.3,0.4]}]}`)
	}))
	defer srv.Close()
	p := NewGemini(GeminiConfig{Name: "gemini", BaseURL: srv.URL, APIKey: "gk"})
	out, err := p.Embed(context.Background(), "emb", &api.EmbeddingRequest{Input: api.StringOrSlice{"a", "b"}})
	require.NoError(t, err)
	require.Equal(t, []float32{0.3, 0.4}, out.Data[1].Embedding)
}
