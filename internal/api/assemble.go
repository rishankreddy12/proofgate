package api

import "strings"

// Assembler rebuilds a ChatResponse from stream chunks (single choice, index 0).
type Assembler struct {
	id, model string
	created   int64
	text      strings.Builder
	tools     []ToolCall
	finish    string
	usage     *Usage
}

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

func (a *Assembler) Text() string { return a.text.String() }

func (a *Assembler) Response() *ChatResponse {
	msg := Message{Role: "assistant", Content: Content{Text: a.text.String()}}
	if len(a.tools) > 0 {
		msg.ToolCalls = append([]ToolCall(nil), a.tools...)
	}
	return &ChatResponse{ID: a.id, Object: "chat.completion", Created: a.created, Model: a.model,
		Choices: []Choice{{Index: 0, Message: msg, FinishReason: a.finish}}, Usage: a.usage}
}

// ChunksFromResponse splits a complete response into stream chunks: role+first text, more text,
// tool calls, a finish chunk and a usage chunk. Used to replay cache hits as streams.
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
