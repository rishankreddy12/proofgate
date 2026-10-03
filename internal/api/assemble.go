// Package api provides enterprise-grade capabilities, configuration, and structural components for the api subsystem.
package api

import "strings"

// Assembler is a stateful buffer used to reconstruct a complete ChatResponse
// from a sequence of streaming ChatChunk responses. This is primarily utilized
// by the gateway to reconstruct the full response payload for telemetry, caching,
// and auditing purposes after a stream has completed returning to the client.
// NOTE: It currently assumes a single choice (Index 0). Multi-choice streams
// will drop data for choices > 0.
type Assembler struct {
	id      string
	model   string
	created int64
	text    strings.Builder
	tools   []ToolCall
	finish  string
	usage   *Usage
}

// Add ingests a new ChatChunk delta into the Assembler's internal buffer.
// It coalesces text streams into a single contiguous string, merges parallel tool
// call deltas into complete arguments, and captures the final usage/finish reasons.
func (a *Assembler) Add(c *ChatChunk) {
	if c.ID != "" && a.id == "" {
		a.id, a.model, a.created = c.ID, c.Model, c.Created
	}
	if c.Usage != nil {
		u := *c.Usage
		a.usage = &u
	}
	for _, ch := range c.Choices {
		if ch.Index != 0 {
			continue
		}
		a.text.WriteString(ch.Delta.Content)
		for _, tc := range ch.Delta.ToolCalls {
			i := 0
			if tc.Index != nil {
				i = *tc.Index
			}
			for len(a.tools) <= i {
				a.tools = append(a.tools, ToolCall{Type: "function"})
			}
			if tc.ID != "" {
				a.tools[i].ID = tc.ID
			}
			if tc.Function.Name != "" {
				a.tools[i].Function.Name = tc.Function.Name
			}
			a.tools[i].Function.Arguments += tc.Function.Arguments
		}
		if ch.FinishReason != nil {
			a.finish = *ch.FinishReason
		}
	}
}

// Text returns the fully assembled contiguous text content collected so far.
// It does not include any function arguments or tool call data.
func (a *Assembler) Text() string { return a.text.String() }

// Response finalizes the state of the Assembler and generates a complete,
// monolithic ChatResponse representing the entirety of the streamed chunks.
// The returned object matches the standard non-streaming OpenAI API output.
func (a *Assembler) Response() *ChatResponse {
	msg := Message{Role: "assistant", Content: Content{Text: a.text.String()}}
	if len(a.tools) > 0 {
		msg.ToolCalls = append([]ToolCall(nil), a.tools...)
	}
	return &ChatResponse{ID: a.id, Object: "chat.completion", Created: a.created, Model: a.model,
		Choices: []Choice{{Index: 0, Message: msg, FinishReason: a.finish}}, Usage: a.usage}
}

// ChunksFromResponse performs the inverse operation of the Assembler.
// It accepts a monolithic ChatResponse and shreds it into an array of strictly-sized ChatChunks.
// This is primarily used by the cache retrieval system to seamlessly replay a
// cached monolithic response back to a client that requested streaming.
func ChunksFromResponse(r *ChatResponse, chunkChars int) []ChatChunk {
	if chunkChars <= 0 {
		chunkChars = 64
	}
	base := ChatChunk{ID: r.ID, Object: "chat.completion.chunk", Created: r.Created, Model: r.Model}
	mk := func(d ChunkDelta, finish *string) ChatChunk {
		c := base
		c.Choices = []ChunkChoice{{Index: 0, Delta: d, FinishReason: finish}}
		return c
	}
	var out []ChatChunk
	if len(r.Choices) == 0 {
		return out
	}
	ch := r.Choices[0]
	text := ch.Message.Content.PlainText()
	first := true
	for len(text) > 0 || first {
		n := min(chunkChars, len(text))
		d := ChunkDelta{Content: text[:n]}
		if first {
			d.Role = "assistant"
			first = false
		}
		out = append(out, mk(d, nil))
		text = text[n:]
	}
	for i, tc := range ch.Message.ToolCalls {
		idx := i
		tc.Index = &idx
		out = append(out, mk(ChunkDelta{ToolCalls: []ToolCall{tc}}, nil))
	}
	fr := ch.FinishReason
	out = append(out, mk(ChunkDelta{}, &fr))
	if r.Usage != nil {
		u := base
		u.Choices = []ChunkChoice{}
		uu := *r.Usage
		u.Usage = &uu
		out = append(out, u)
	}
	return out
}
