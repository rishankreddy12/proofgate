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

type OpenAIConfig struct {
	Name    string
	BaseURL string // e.g. https://api.openai.com/v1 or http://ollama:11434/v1
	APIKey  string
	Headers map[string]string
}

type OpenAI struct {
	cfg    OpenAIConfig
	client *http.Client
}

func NewOpenAI(cfg OpenAIConfig) *OpenAI {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &OpenAI{cfg: cfg, client: newClient()}
}

func (p *OpenAI) Name() string { return p.cfg.Name }

func (p *OpenAI) post(ctx context.Context, path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	for k, v := range p.cfg.Headers {
		req.Header.Set(k, v)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, netError(ctx, p.cfg.Name, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, FromHTTP(p.cfg.Name, resp)
	}
	return resp, nil
}

// withModel returns a shallow copy with the target model. Slices are shared but never mutated.
func withModel(req *api.ChatRequest, model string, stream bool) api.ChatRequest {
	out := *req
	out.Model = model
	out.Stream = stream
	out.StreamOptions = nil
	if stream {
		out.StreamOptions = &api.StreamOptions{IncludeUsage: true} // always ask for usage so billing is exact
	}
	return out
}

func (p *OpenAI) Chat(ctx context.Context, model string, req *api.ChatRequest) (*api.ChatResponse, error) {
	resp, err := p.post(ctx, "/chat/completions", withModel(req, model, false))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out api.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, &Error{Provider: p.cfg.Name, Status: 502, Message: "bad JSON from upstream: " + err.Error(), Retryable: true}
	}
	out.Model = model
	return &out, nil
}

func (p *OpenAI) ChatStream(ctx context.Context, model string, req *api.ChatRequest) (Stream, error) {
	ctx, cancel := context.WithCancel(ctx)
	resp, err := p.post(ctx, "/chat/completions", withModel(req, model, true))
	if err != nil {
		cancel()
		return nil, err
	}
	return &openAIStream{name: p.cfg.Name, model: model, body: resp.Body, r: sse.NewReader(resp.Body), cancel: cancel}, nil
}

type openAIStream struct {
	name, model string
	body        io.ReadCloser
	r           *sse.Reader
	cancel      context.CancelFunc
}

func (s *openAIStream) Recv() (*api.ChatChunk, error) {
	for {
		ev, err := s.r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			return nil, &Error{Provider: s.name, Message: "stream read: " + err.Error(), Retryable: true}
		}
		if string(ev.Data) == "[DONE]" {
			return nil, io.EOF
		}
		if len(ev.Data) == 0 {
			continue
		}
		var c api.ChatChunk
		if err := json.Unmarshal(ev.Data, &c); err != nil {
			return nil, &Error{Provider: s.name, Status: 502, Message: "bad chunk: " + err.Error(), Retryable: true}
		}
		c.Model = s.model
		return &c, nil
	}
}

func (s *openAIStream) Close() error {
	s.cancel()
	return s.body.Close()
}

func (p *OpenAI) Embed(ctx context.Context, model string, req *api.EmbeddingRequest) (*api.EmbeddingResponse, error) {
	body := *req
	body.Model = model
	resp, err := p.post(ctx, "/embeddings", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out api.EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, &Error{Provider: p.cfg.Name, Status: 502, Message: "bad JSON: " + err.Error(), Retryable: true}
	}
	out.Model = model
	return &out, nil
}
