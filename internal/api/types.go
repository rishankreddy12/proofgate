// Package api defines the OpenAI-compatible wire types that ProofGate exposes.
// This package is responsible for standardizing the structures used for cross-provider
// communication. Types here are marshaled/unmarshaled directly from HTTP JSON payloads.
package api

import (
	"encoding/json"
	"strings"
)

// Message represents a single conversational turn in a ChatRequest or ChatResponse.
type Message struct {
	// Role is the author of the message (e.g., "system", "user", "assistant", "tool").
	Role string `json:"role"`
	// Content contains the message body (can be text or multimodal).
	Content Content `json:"content"`
	// Name optionally identifies the specific participant if there are multiple.
	Name string `json:"name,omitempty"`
	// ToolCalls specifies functions invoked by the assistant.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID links a "tool" role message back to the ToolCall that invoked it.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// Tool defines an external capability provided to the model.
type Tool struct {
	// Type must be "function".
	Type string `json:"type"`
	// Function defines the signature of the capability.
	Function FunctionDef `json:"function"`
}

// FunctionDef defines the signature and schema of a single tool.
type FunctionDef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Parameters is a JSON schema describing the expected arguments.
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall represents an invocation of a tool by the model.
type ToolCall struct {
	// Index is only populated during streaming to indicate the chunk position.
	Index *int `json:"index,omitempty"`
	// ID uniquely identifies this tool call invocation.
	ID string `json:"id,omitempty"`
	// Type is always "function".
	Type string `json:"type,omitempty"`
	// Function contains the actual execution data.
	Function FunctionCall `json:"function"`
}

// FunctionCall contains the name and arguments for a specific tool execution.
type FunctionCall struct {
	Name string `json:"name,omitempty"`
	// Arguments is a stringified JSON object matching the Tool's Parameters schema.
	Arguments string `json:"arguments"`
}

// StreamOptions provides advanced configuration for streaming responses.
type StreamOptions struct {
	// IncludeUsage dictates whether the final chunk of a stream should include a Usage block.
	IncludeUsage bool `json:"include_usage"`
}

// ChatRequest represents the complete payload sent by a client to generate a chat completion.
// It rigidly adheres to the OpenAI chat/completions API schema.
type ChatRequest struct {
	Model               string          `json:"model"`
	Messages            []Message       `json:"messages"`
	Stream              bool            `json:"stream,omitempty"`
	StreamOptions       *StreamOptions  `json:"stream_options,omitempty"`
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	Stop                StringOrSlice   `json:"stop,omitempty"`
	Tools               []Tool          `json:"tools,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
	ResponseFormat      json.RawMessage `json:"response_format,omitempty"`
	Seed                *int            `json:"seed,omitempty"`
	User                string          `json:"user,omitempty"`
	N                   *int            `json:"n,omitempty"`
	PresencePenalty     *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float64        `json:"frequency_penalty,omitempty"`
	ParallelToolCalls   *bool           `json:"parallel_tool_calls,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	Logprobs            *bool           `json:"logprobs,omitempty"`
	TopLogprobs         *int            `json:"top_logprobs,omitempty"`
}

// PromptTokensDetails provides granular telemetry on caching efficiency.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// Usage reports the exact token consumption of a request.
type Usage struct {
	PromptTokens        int                  `json:"prompt_tokens"`
	CompletionTokens    int                  `json:"completion_tokens"`
	TotalTokens         int                  `json:"total_tokens"`
	PromptTokensDetails *PromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

// CachedTokens is a helper to safely retrieve the number of tokens served from provider-side cache.
func (u Usage) CachedTokens() int {
	if u.PromptTokensDetails == nil {
		return 0
	}
	return u.PromptTokensDetails.CachedTokens
}

// Choice represents one generated candidate from the model.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// ChatResponse represents a monolithic, non-streaming reply from the LLM Gateway.
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// ChunkDelta represents the incremental data received in a single stream event.
type ChunkDelta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ChunkChoice wraps a ChunkDelta with its stream context.
type ChunkChoice struct {
	Index        int        `json:"index"`
	Delta        ChunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

// ChatChunk represents a single Server-Sent Event (SSE) payload during a streaming response.
type ChatChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
	Usage   *Usage        `json:"usage,omitempty"`
}

// EmbeddingRequest defines the payload for text-to-vector generation.
type EmbeddingRequest struct {
	Model          string        `json:"model"`
	Input          StringOrSlice `json:"input"`
	EncodingFormat string        `json:"encoding_format,omitempty"`
	Dimensions     *int          `json:"dimensions,omitempty"`
	User           string        `json:"user,omitempty"`
}

// Embedding represents a single generated vector.
type Embedding struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// EmbeddingResponse represents the full output of a vector generation request.
type EmbeddingResponse struct {
	Object string      `json:"object"`
	Data   []Embedding `json:"data"`
	Model  string      `json:"model"`
	Usage  Usage       `json:"usage"`
}

// PromptText renders all messages into a unified block of "role: text" lines.
// This flattened string is heavily relied upon for token estimation, semantic caching deduplication,
// and prompt-injection guardrail scanning.
func (r *ChatRequest) PromptText() string {
	var b strings.Builder
	for i, m := range r.Messages {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content.PlainText())
		for _, tc := range m.ToolCalls {
			b.WriteString(" [tool_call " + tc.Function.Name + " " + tc.Function.Arguments + "]")
		}
	}
	return b.String()
}

// EffectiveMaxTokens normalizes the output token limit.
// It prioritizes max_completion_tokens (O1 schema), then max_tokens (Legacy schema),
// falling back to a provided default if neither is set.
func (r *ChatRequest) EffectiveMaxTokens(def int) int {
	if r.MaxCompletionTokens != nil && *r.MaxCompletionTokens > 0 {
		return *r.MaxCompletionTokens
	}
	if r.MaxTokens != nil && *r.MaxTokens > 0 {
		return *r.MaxTokens
	}
	return def
}

// Clone returns a deep copy of the ChatRequest.
// It uses a JSON round-trip to guarantee absolute structural integrity if nested schema fields are added.
// This is not on the hot path; it is used specifically for guardrail redaction and background shadow calls
// where mutating the original request pointer would trigger race conditions.
func (r *ChatRequest) Clone() *ChatRequest {
	b, err := json.Marshal(r)
	if err != nil {
		panic(err) // all fields are marshalable
	}
	var c ChatRequest
	if err := json.Unmarshal(b, &c); err != nil {
		panic(err)
	}
	return &c
}

// Clone returns a deep copy of the Message.
// This is necessary to prevent data races when background agents modify tool calls or content in-flight.
func (m Message) Clone() Message {
	cp := m
	cp.Content = m.Content.Clone()
	if m.ToolCalls != nil {
		cp.ToolCalls = make([]ToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			cp.ToolCalls[i] = tc
			if tc.Index != nil {
				idx := *tc.Index
				cp.ToolCalls[i].Index = &idx
			}
		}
	}
	return cp
}

// Clone returns a deep copy of the ChatResponse.
func (r *ChatResponse) Clone() *ChatResponse {
	if r == nil {
		return nil
	}
	cp := *r
	cp.Choices = make([]Choice, len(r.Choices))
	for i, ch := range r.Choices {
		cp.Choices[i] = ch
		cp.Choices[i].Message = ch.Message.Clone()
	}
	if r.Usage != nil {
		u := *r.Usage
		if r.Usage.PromptTokensDetails != nil {
			ptd := *r.Usage.PromptTokensDetails
			u.PromptTokensDetails = &ptd
		}
		cp.Usage = &u
	}
	return &cp
}
