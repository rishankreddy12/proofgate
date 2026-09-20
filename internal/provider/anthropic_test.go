package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/stretchr/testify/require"
)

func TestToAnthropicTranslation(t *testing.T) {
	req := &api.ChatRequest{
		Messages: []api.Message{
			{Role: "system", Content: api.Content{Text: "rule 1"}},
			{Role: "system", Content: api.Content{Text: "rule 2"}},
			{Role: "user", Content: api.Content{Text: "weather?"}},
			{Role: "assistant", ToolCalls: []api.ToolCall{{ID: "t1", Type: "function", Function: api.FunctionCall{Name: "wx", Arguments: `{"city":"Pune"}`}}}},
			{Role: "tool", ToolCallID: "t1", Content: api.Content{Text: "31C"}},
			{Role: "user", Content: api.Content{Text: "thanks"}},
		},
		Stop:       api.StringOrSlice{"END"},
		Tools:      []api.Tool{{Type: "function", Function: api.FunctionDef{Name: "wx", Parameters: json.RawMessage(`{"type":"object"}`)}}},
		ToolChoice: json.RawMessage(`"required"`),
	}
	out, err := toAnthropic("claude-x", req, false)
	require.NoError(t, err)
	b, _ := json.Marshal(out)
	require.JSONEq(t, `{
	  "model":"claude-x","max_tokens":4096,"system":"rule 1\n\nrule 2","stop_sequences":["END"],
	  "messages":[
	    {"role":"user","content":[{"type":"text","text":"weather?"}]},
	    {"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"wx","input":{"city":"Pune"}}]},
	    {"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"31C"},{"type":"text","text":"thanks"}]}
	  ],
	  "tools":[{"name":"wx","input_schema":{"type":"object"}}],
	  "tool_choice":{"type":"any"}
	}`, string(b))
}

func TestAnthropicChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Equal(t, "k", r.Header.Get("x-api-key"))
		require.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
		fmt.Fprint(w, `{"id":"msg_1","model":"claude-x","stop_reason":"tool_use",
		  "content":[{"type":"text","text":"Checking."},{"type":"tool_use","id":"t9","name":"wx","input":{"city":"Pune"}}],
		  "usage":{"input_tokens":10,"cache_read_input_tokens":5,"cache_creation_input_tokens":0,"output_tokens":7}}`)
	}))
	defer srv.Close()
	p := NewAnthropic(AnthropicConfig{Name: "anthropic", BaseURL: srv.URL, APIKey: "k"})
	resp, err := p.Chat(context.Background(), "claude-x", userReq("hi", 50))
	require.NoError(t, err)
	c := resp.Choices[0]
	require.Equal(t, "Checking.", c.Message.Content.Text)
	require.Equal(t, "tool_calls", c.FinishReason)
	require.Equal(t, `{"city":"Pune"}`, c.Message.ToolCalls[0].Function.Arguments)
	require.Equal(t, 15, resp.Usage.PromptTokens)
	require.Equal(t, 5, resp.Usage.CachedTokens())
	require.Equal(t, 22, resp.Usage.TotalTokens)
}

const anthropicSSE = `event: message_start
data: {"type":"message_start","message":{"id":"msg_2","model":"claude-x","usage":{"input_tokens":12,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type":"ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"there"}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"wx","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"Pune\"}"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}

event: message_stop
data: {"type":"message_stop"}

`

func TestAnthropicStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		require.Equal(t, true, body["stream"])
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, anthropicSSE)
	}))
	defer srv.Close()
	p := NewAnthropic(AnthropicConfig{Name: "anthropic", BaseURL: srv.URL, APIKey: "k"})
	st, err := p.ChatStream(context.Background(), "claude-x", userReq("hi", 50))
	require.NoError(t, err)
	defer st.Close()
	r := drain(t, st).Response()
	require.Equal(t, "Hi there", r.Choices[0].Message.Content.Text)
	require.Equal(t, "tool_calls", r.Choices[0].FinishReason)
	require.Equal(t, "t1", r.Choices[0].Message.ToolCalls[0].ID)
	require.Equal(t, `{"city":"Pune"}`, r.Choices[0].Message.ToolCalls[0].Function.Arguments)
	require.Equal(t, 12, r.Usage.PromptTokens)
	require.Equal(t, 9, r.Usage.CompletionTokens)
}

func TestAnthropicStreamOverloaded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	}))
	defer srv.Close()
	p := NewAnthropic(AnthropicConfig{Name: "anthropic", BaseURL: srv.URL, APIKey: "k"})
	st, err := p.ChatStream(context.Background(), "claude-x", userReq("hi", 5))
	require.NoError(t, err)
	_, err = st.Recv()
	var pe *Error
	require.ErrorAs(t, err, &pe)
	require.Equal(t, 529, pe.Status)
	require.True(t, pe.Retryable)
}
