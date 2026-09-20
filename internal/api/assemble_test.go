package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }

func TestAssemblerTextAndTools(t *testing.T) {
	var a Assembler
	a.Add(&ChatChunk{ID: "x", Model: "m", Created: 1, Choices: []ChunkChoice{{Delta: ChunkDelta{Role: "assistant", Content: "Hel"}}}})
	a.Add(&ChatChunk{Choices: []ChunkChoice{{Delta: ChunkDelta{Content: "lo"}}}})
	a.Add(&ChatChunk{Choices: []ChunkChoice{{Delta: ChunkDelta{ToolCalls: []ToolCall{{Index: ptr(0), ID: "c1", Type: "function", Function: FunctionCall{Name: "get", Arguments: `{"a":`}}}}}}})
	a.Add(&ChatChunk{Choices: []ChunkChoice{{Delta: ChunkDelta{ToolCalls: []ToolCall{{Index: ptr(0), Function: FunctionCall{Arguments: `1}`}}}}}}})
	a.Add(&ChatChunk{Choices: []ChunkChoice{{FinishReason: ptr("tool_calls")}}})
	a.Add(&ChatChunk{Usage: &Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}})

	r := a.Response()
	require.Equal(t, "x", r.ID)
	require.Equal(t, "chat.completion", r.Object)
	require.Equal(t, "Hello", r.Choices[0].Message.Content.Text)
	require.Equal(t, `{"a":1}`, r.Choices[0].Message.ToolCalls[0].Function.Arguments)
	require.Nil(t, r.Choices[0].Message.ToolCalls[0].Index)
	require.Equal(t, "tool_calls", r.Choices[0].FinishReason)
	require.Equal(t, 5, r.Usage.TotalTokens)
	require.Equal(t, "Hello", a.Text())
}

func TestChunksFromResponseRoundTrip(t *testing.T) {
	resp := &ChatResponse{ID: "r", Model: "m", Created: 9, Choices: []Choice{{Message: Message{Role: "assistant", Content: Content{Text: "abcdefghij"}}, FinishReason: "stop"}}, Usage: &Usage{TotalTokens: 7}}
	chunks := ChunksFromResponse(resp, 4)
	require.Len(t, chunks, 5) // role+abcd, efgh, ij, finish, usage
	var a Assembler
	for i := range chunks {
		a.Add(&chunks[i])
	}
	require.Equal(t, "abcdefghij", a.Response().Choices[0].Message.Content.Text)
	require.Equal(t, "stop", a.Response().Choices[0].FinishReason)
	require.Equal(t, 7, a.Response().Usage.TotalTokens)
}
