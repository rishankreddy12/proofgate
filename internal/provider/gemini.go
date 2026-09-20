package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/sse"
)

type GeminiConfig struct {
	Name    string
	BaseURL string // https://generativelanguage.googleapis.com
	APIKey  string
}

type Gemini struct {
	cfg    GeminiConfig
	client *http.Client
}

func NewGemini(cfg GeminiConfig) *Gemini {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Gemini{cfg: cfg, client: newClient()}
}

func (p *Gemini) Name() string { return p.cfg.Name }

type gPart struct {
	Text             string        `json:"text,omitempty"`
	FunctionCall     *gFuncCall    `json:"functionCall,omitempty"`
	FunctionResponse *gFuncResp    `json:"functionResponse,omitempty"`
	InlineData       *gInlineData  `json:"inlineData,omitempty"`
}
type gFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}
type gFuncResp struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}
type gInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}
type gContent struct {
	Role  string  `json:"role,omitempty"`
	Parts []gPart `json:"parts"`
}
type gGenConfig struct {
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}
type gFuncDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}
type gTool struct {
	FunctionDeclarations []gFuncDecl `json:"functionDeclarations"`
}
type geminiRequest struct {
	SystemInstruction *gContent   `json:"systemInstruction,omitempty"`
	Contents          []gContent  `json:"contents"`
	GenerationConfig  *gGenConfig `json:"generationConfig,omitempty"`
	Tools             []gTool     `json:"tools,omitempty"`
}

// cleanSchema removes JSON Schema keywords that Gemini rejects.
func cleanSchema(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	var walk func(any) any
	walk = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			delete(t, "$schema")
			delete(t, "additionalProperties")
			for k, vv := range t {
				t[k] = walk(vv)
			}
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
		}
		return x
	}
	return json.Marshal(walk(v))
}

func toGemini(req *api.ChatRequest) (geminiRequest, error) {
	var out geminiRequest
	names := map[string]string{} // tool call id -> function name
	var system []gPart
	add := func(role string, parts ...gPart) {
		if n := len(out.Contents); n > 0 && out.Contents[n-1].Role == role {
			out.Contents[n-1].Parts = append(out.Contents[n-1].Parts, parts...)
			return
		}
		out.Contents = append(out.Contents, gContent{Role: role, Parts: parts})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "system", "developer":
			system = append(system, gPart{Text: m.Content.PlainText()})
		case "user":
			if m.Content.Parts == nil {
				add("user", gPart{Text: m.Content.Text})
				continue
			}
			for _, p := range m.Content.Parts {
				if p.Type == "text" {
					add("user", gPart{Text: p.Text})
				} else if p.Type == "image_url" && p.ImageURL != nil && strings.HasPrefix(p.ImageURL.URL, "data:") {
					meta, data, _ := strings.Cut(strings.TrimPrefix(p.ImageURL.URL, "data:"), ",")
					add("user", gPart{InlineData: &gInlineData{MimeType: strings.TrimSuffix(meta, ";base64"), Data: data}})
				}
			}
		case "assistant":
			var parts []gPart
			if t := m.Content.PlainText(); t != "" {
				parts = append(parts, gPart{Text: t})
			}
			for _, tc := range m.ToolCalls {
				names[tc.ID] = tc.Function.Name
				args := json.RawMessage(tc.Function.Arguments)
				if len(bytes.TrimSpace(args)) == 0 {
					args = json.RawMessage(`{}`)
				}
				parts = append(parts, gPart{FunctionCall: &gFuncCall{Name: tc.Function.Name, Args: args}})
			}
			if len(parts) > 0 {
				add("model", parts...)
			}
		case "tool":
			name, ok := names[m.ToolCallID]
			if !ok {
				return out, api.BadRequest("tool message references unknown tool_call_id " + m.ToolCallID)
			}
			add("user", gPart{FunctionResponse: &gFuncResp{Name: name, Response: map[string]any{"content": m.Content.PlainText()}}})
		default:
			return out, api.BadRequest("unsupported role: " + m.Role)
		}
	}
	if len(system) > 0 {
		out.SystemInstruction = &gContent{Parts: system}
	}
	gc := gGenConfig{Temperature: req.Temperature, TopP: req.TopP, StopSequences: req.Stop}
	if n := req.EffectiveMaxTokens(0); n > 0 {
		gc.MaxOutputTokens = &n
	}
	if gc.MaxOutputTokens != nil || gc.Temperature != nil || gc.TopP != nil || len(gc.StopSequences) > 0 {
		out.GenerationConfig = &gc
	}
	if len(req.Tools) > 0 {
		var decls []gFuncDecl
		for _, t := range req.Tools {
			params, err := cleanSchema(t.Function.Parameters)
			if err != nil {
				return out, api.BadRequest("invalid tool parameters schema")
			}
			decls = append(decls, gFuncDecl{Name: t.Function.Name, Description: t.Function.Description, Parameters: params})
		}
		out.Tools = []gTool{{FunctionDeclarations: decls}}
	}
	return out, nil
}

type gResponse struct {
	Candidates []struct {
		Content      gContent `json:"content"`
		FinishReason string   `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
}

func (r *gResponse) usage() *api.Usage {
	if r.UsageMetadata == nil {
		return nil
	}
	m := r.UsageMetadata
	return &api.Usage{PromptTokens: m.PromptTokenCount, CompletionTokens: m.CandidatesTokenCount,
		TotalTokens: m.PromptTokenCount + m.CandidatesTokenCount,
		PromptTokensDetails: &api.PromptTokensDetails{CachedTokens: m.CachedContentTokenCount}}
}

func geminiFinish(reason string, hasTools bool) string {
	if hasTools {
		return "tool_calls"
	}
	switch reason {
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII":
		return "content_filter"
	case "":
		return ""
	default:
		return "stop"
	}
}

func (p *Gemini) post(ctx context.Context, path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", p.cfg.APIKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, netError(ctx, p.cfg.Name, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, FromHTTP(p.cfg.Name, resp)
	}
	return resp, nil
}

func (p *Gemini) Chat(ctx context.Context, model string, req *api.ChatRequest) (*api.ChatResponse, error) {
	body, err := toGemini(req)
	if err != nil {
		return nil, err
	}
	resp, err := p.post(ctx, "/v1beta/models/"+model+":generateContent", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var gr gResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, &Error{Provider: p.cfg.Name, Status: 502, Message: "bad JSON: " + err.Error(), Retryable: true}
	}
	msg := api.Message{Role: "assistant"}
	finish := "stop"
	if len(gr.Candidates) > 0 {
		c := gr.Candidates[0]
		var text strings.Builder
		for _, part := range c.Content.Parts {
			text.WriteString(part.Text)
			if part.FunctionCall != nil {
				args := string(part.FunctionCall.Args)
				if args == "" {
					args = "{}"
				}
				msg.ToolCalls = append(msg.ToolCalls, api.ToolCall{ID: fmt.Sprintf("call_%d", len(msg.ToolCalls)), Type: "function",
					Function: api.FunctionCall{Name: part.FunctionCall.Name, Arguments: args}})
			}
		}
		msg.Content = api.Content{Text: text.String()}
		finish = geminiFinish(c.FinishReason, len(msg.ToolCalls) > 0)
	}
	return &api.ChatResponse{Object: "chat.completion", Model: model,
		Choices: []api.Choice{{Message: msg, FinishReason: finish}}, Usage: gr.usage()}, nil
}

func (p *Gemini) ChatStream(ctx context.Context, model string, req *api.ChatRequest) (Stream, error) {
	body, err := toGemini(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	resp, err := p.post(ctx, "/v1beta/models/"+model+":streamGenerateContent?alt=sse", body)
	if err != nil {
		cancel()
		return nil, err
	}
	return &geminiStream{name: p.cfg.Name, model: model, body: resp.Body, r: sse.NewReader(resp.Body), cancel: cancel}, nil
}

type geminiStream struct {
	name, model string
	body        io.ReadCloser
	r           *sse.Reader
	cancel      context.CancelFunc
	started     bool
	tools       int
	usage       *api.Usage
	finished    bool
}

func (s *geminiStream) Recv() (*api.ChatChunk, error) {
	for {
		ev, err := s.r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if s.usage != nil && !s.finished {
					s.finished = true
					return &api.ChatChunk{Object: "chat.completion.chunk", Model: s.model, Choices: []api.ChunkChoice{}, Usage: s.usage}, nil
				}
				return nil, io.EOF
			}
			return nil, &Error{Provider: s.name, Message: "stream read: " + err.Error(), Retryable: true}
		}
		var gr gResponse
		if err := json.Unmarshal(ev.Data, &gr); err != nil {
			return nil, &Error{Provider: s.name, Status: 502, Message: "bad chunk: " + err.Error(), Retryable: true}
		}
		if u := gr.usage(); u != nil {
			s.usage = u
		}
		if len(gr.Candidates) == 0 {
			continue
		}
		c := gr.Candidates[0]
		d := api.ChunkDelta{}
		if !s.started {
			d.Role = "assistant"
			s.started = true
		}
		for _, part := range c.Content.Parts {
			d.Content += part.Text
			if part.FunctionCall != nil {
				i := s.tools
				s.tools++
				args := string(part.FunctionCall.Args)
				if args == "" {
					args = "{}"
				}
				d.ToolCalls = append(d.ToolCalls, api.ToolCall{Index: &i, ID: fmt.Sprintf("call_%d", i), Type: "function",
					Function: api.FunctionCall{Name: part.FunctionCall.Name, Arguments: args}})
			}
		}
		var fr *string
		if f := geminiFinish(c.FinishReason, s.tools > 0 && c.FinishReason != ""); f != "" {
			fr = &f
		}
		return &api.ChatChunk{Object: "chat.completion.chunk", Model: s.model, Choices: []api.ChunkChoice{{Delta: d, FinishReason: fr}}}, nil
	}
}

func (s *geminiStream) Close() error {
	s.cancel()
	return s.body.Close()
}

func (p *Gemini) Embed(ctx context.Context, model string, req *api.EmbeddingRequest) (*api.EmbeddingResponse, error) {
	type item struct {
		Model   string   `json:"model"`
		Content gContent `json:"content"`
	}
	var body struct {
		Requests []item `json:"requests"`
	}
	for _, in := range req.Input {
		body.Requests = append(body.Requests, item{Model: "models/" + model, Content: gContent{Parts: []gPart{{Text: in}}}})
	}
	resp, err := p.post(ctx, "/v1beta/models/"+model+":batchEmbedContents", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var gr struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, &Error{Provider: p.cfg.Name, Status: 502, Message: "bad JSON: " + err.Error(), Retryable: true}
	}
	out := &api.EmbeddingResponse{Object: "list", Model: model}
	tokens := 0
	for i, e := range gr.Embeddings {
		out.Data = append(out.Data, api.Embedding{Object: "embedding", Index: i, Embedding: e.Values})
	}
	for _, in := range req.Input {
		tokens += api.EstimateTokens(in) // Gemini batch embed does not report usage
	}
	out.Usage = api.Usage{PromptTokens: tokens, TotalTokens: tokens}
	return out, nil
}
