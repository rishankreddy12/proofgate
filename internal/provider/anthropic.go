package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/sse"
)

type AnthropicConfig struct {
	Name    string
	BaseURL string // https://api.anthropic.com
	APIKey  string
	KeyFunc KeyFunc
}

type Anthropic struct {
	cfg    AnthropicConfig
	client *http.Client
}

func NewAnthropic(cfg AnthropicConfig) *Anthropic {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Anthropic{cfg: cfg, client: newClient()}
}

func (p *Anthropic) Name() string { return p.cfg.Name }

type aBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	Source    *aSource        `json:"source,omitempty"`
}

type aSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type aMessage struct {
	Role    string   `json:"role"`
	Content []aBlock `json:"content"`
}

type aTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	System        string          `json:"system,omitempty"`
	Messages      []aMessage      `json:"messages"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	Tools         []aTool         `json:"tools,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
}

func imageBlock(u string) aBlock {
	if strings.HasPrefix(u, "data:") { // data:image/png;base64,XXXX
		meta, data, _ := strings.Cut(strings.TrimPrefix(u, "data:"), ",")
		return aBlock{Type: "image", Source: &aSource{Type: "base64", MediaType: strings.TrimSuffix(meta, ";base64"), Data: data}}
	}
	return aBlock{Type: "image", Source: &aSource{Type: "url", URL: u}}
}

func toAnthropic(model string, req *api.ChatRequest, stream bool) (anthropicRequest, error) {
	out := anthropicRequest{Model: model, MaxTokens: req.EffectiveMaxTokens(4096), Temperature: req.Temperature,
		TopP: req.TopP, StopSequences: req.Stop, Stream: stream}
	var system []string
	add := func(role string, blocks ...aBlock) {
		if n := len(out.Messages); n > 0 && out.Messages[n-1].Role == role {
			out.Messages[n-1].Content = append(out.Messages[n-1].Content, blocks...)
			return
		}
		out.Messages = append(out.Messages, aMessage{Role: role, Content: blocks})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "system", "developer":
			system = append(system, m.Content.PlainText())
		case "user":
			if m.Content.Parts == nil {
				add("user", aBlock{Type: "text", Text: m.Content.Text})
				continue
			}
			for _, p := range m.Content.Parts {
				switch p.Type {
				case "text":
					add("user", aBlock{Type: "text", Text: p.Text})
				case "image_url":
					if p.ImageURL != nil {
						add("user", imageBlock(p.ImageURL.URL))
					}
				}
			}
		case "assistant":
			var blocks []aBlock
			if t := m.Content.PlainText(); t != "" {
				blocks = append(blocks, aBlock{Type: "text", Text: t})
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Function.Arguments)
				if len(bytes.TrimSpace(input)) == 0 {
					input = json.RawMessage(`{}`)
				}
				if !json.Valid(input) {
					return out, api.BadRequest("tool call arguments are not valid JSON")
				}
				blocks = append(blocks, aBlock{Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: input})
			}
			if len(blocks) > 0 {
				add("assistant", blocks...)
			}
		case "tool":
			add("user", aBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content.PlainText()})
		default:
			return out, api.BadRequest("unsupported role: " + m.Role)
		}
	}
	out.System = strings.Join(system, "\n\n")
	for _, t := range req.Tools {
		schema := t.Function.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out.Tools = append(out.Tools, aTool{Name: t.Function.Name, Description: t.Function.Description, InputSchema: schema})
	}
	if len(req.ToolChoice) > 0 {
		var s string
		if json.Unmarshal(req.ToolChoice, &s) == nil {
			switch s {
			case "auto":
				out.ToolChoice = json.RawMessage(`{"type":"auto"}`)
			case "required":
				out.ToolChoice = json.RawMessage(`{"type":"any"}`)
			case "none":
				out.ToolChoice = json.RawMessage(`{"type":"none"}`)
			}
		} else {
			var obj struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(req.ToolChoice, &obj) == nil && obj.Function.Name != "" {
				b, _ := json.Marshal(map[string]string{"type": "tool", "name": obj.Function.Name})
				out.ToolChoice = b
			}
		}
	}
	return out, nil
}

type aUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func (u aUsage) toUsage() *api.Usage {
	prompt := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	return &api.Usage{PromptTokens: prompt, CompletionTokens: u.OutputTokens, TotalTokens: prompt + u.OutputTokens,
		PromptTokensDetails: &api.PromptTokensDetails{CachedTokens: u.CacheReadInputTokens}}
}

func finishReason(stop string) string {
	switch stop {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	default:
		return "stop"
	}
}

func (p *Anthropic) post(ctx context.Context, body anthropicRequest) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/v1/messages", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	key := p.cfg.APIKey
	if p.cfg.KeyFunc != nil {
		k, err := p.cfg.KeyFunc(ctx)
		if err != nil {
			return nil, &Error{Provider: p.cfg.Name, Status: 401, Message: "credential unavailable", Retryable: false}
		}
		key = k
	}
	if key != "" {
		req.Header.Set("x-api-key", key)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, netError(ctx, p.cfg.Name, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, FromHTTP(p.cfg.Name, resp)
	}
	return resp, nil
}

func (p *Anthropic) Chat(ctx context.Context, model string, req *api.ChatRequest) (*api.ChatResponse, error) {
	body, err := toAnthropic(model, req, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.post(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var ar struct {
		ID         string   `json:"id"`
		StopReason string   `json:"stop_reason"`
		Content    []aBlock `json:"content"`
		Usage      aUsage   `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return nil, &Error{Provider: p.cfg.Name, Status: 502, Message: "bad JSON: " + err.Error(), Retryable: true}
	}
	msg := api.Message{Role: "assistant"}
	var text strings.Builder
	for _, b := range ar.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			msg.ToolCalls = append(msg.ToolCalls, api.ToolCall{ID: b.ID, Type: "function",
				Function: api.FunctionCall{Name: b.Name, Arguments: string(b.Input)}})
		}
	}
	msg.Content = api.Content{Text: text.String()}
	return &api.ChatResponse{ID: ar.ID, Object: "chat.completion", Model: model,
		Choices: []api.Choice{{Message: msg, FinishReason: finishReason(ar.StopReason)}}, Usage: ar.Usage.toUsage()}, nil
}

func (p *Anthropic) ChatStream(ctx context.Context, model string, req *api.ChatRequest) (Stream, error) {
	body, err := toAnthropic(model, req, true)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	resp, err := p.post(ctx, body)
	if err != nil {
		cancel()
		return nil, err
	}
	return &anthropicStream{name: p.cfg.Name, model: model, body: resp.Body, r: sse.NewReader(resp.Body),
		cancel: cancel, toolIndex: map[int]int{}}, nil
}

type anthropicStream struct {
	name, model string
	body        io.ReadCloser
	r           *sse.Reader
	cancel      context.CancelFunc
	id          string
	usage       aUsage
	toolIndex   map[int]int // content block index -> OpenAI tool call index
	done        bool
}

type aEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		ID    string `json:"id"`
		Usage aUsage `json:"usage"`
	} `json:"message"`
	ContentBlock aBlock `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage aUsage `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (s *anthropicStream) chunk(d api.ChunkDelta, finish *string) *api.ChatChunk {
	return &api.ChatChunk{ID: s.id, Object: "chat.completion.chunk", Model: s.model,
		Choices: []api.ChunkChoice{{Delta: d, FinishReason: finish}}}
}

func (s *anthropicStream) Recv() (*api.ChatChunk, error) {
	for {
		if s.done {
			return nil, io.EOF
		}
		ev, err := s.r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			return nil, &Error{Provider: s.name, Message: "stream read: " + err.Error(), Retryable: true}
		}
		var e aEvent
		if err := json.Unmarshal(ev.Data, &e); err != nil {
			return nil, &Error{Provider: s.name, Status: 502, Message: "bad event: " + err.Error(), Retryable: true}
		}
		switch e.Type {
		case "message_start":
			s.id = e.Message.ID
			s.usage.InputTokens = e.Message.Usage.InputTokens
			s.usage.CacheReadInputTokens = e.Message.Usage.CacheReadInputTokens
			s.usage.CacheCreationInputTokens = e.Message.Usage.CacheCreationInputTokens
			return s.chunk(api.ChunkDelta{Role: "assistant"}, nil), nil
		case "content_block_start":
			if e.ContentBlock.Type == "tool_use" {
				i := len(s.toolIndex)
				s.toolIndex[e.Index] = i
				return s.chunk(api.ChunkDelta{ToolCalls: []api.ToolCall{{Index: &i, ID: e.ContentBlock.ID, Type: "function",
					Function: api.FunctionCall{Name: e.ContentBlock.Name}}}}, nil), nil
			}
		case "content_block_delta":
			switch e.Delta.Type {
			case "text_delta":
				return s.chunk(api.ChunkDelta{Content: e.Delta.Text}, nil), nil
			case "input_json_delta":
				i := s.toolIndex[e.Index]
				return s.chunk(api.ChunkDelta{ToolCalls: []api.ToolCall{{Index: &i,
					Function: api.FunctionCall{Arguments: e.Delta.PartialJSON}}}}, nil), nil
			}
		case "message_delta":
			s.usage.OutputTokens = e.Usage.OutputTokens
			fr := finishReason(e.Delta.StopReason)
			return s.chunk(api.ChunkDelta{}, &fr), nil
		case "message_stop":
			s.done = true
			return &api.ChatChunk{ID: s.id, Object: "chat.completion.chunk", Model: s.model,
				Choices: []api.ChunkChoice{}, Usage: s.usage.toUsage()}, nil
		case "error":
			status := 500
			if e.Error.Type == "overloaded_error" {
				status = 529
			}
			return nil, &Error{Provider: s.name, Status: status, Message: e.Error.Message, Retryable: true}
		}
		// ping, content_block_stop and unknown events are skipped
	}
}

func (s *anthropicStream) Close() error {
	s.cancel()
	return s.body.Close()
}

func (p *Anthropic) Embed(context.Context, string, *api.EmbeddingRequest) (*api.EmbeddingResponse, error) {
	return nil, &Error{Provider: p.cfg.Name, Status: 400, Message: "anthropic does not offer embeddings"}
}
