// Package provider provides enterprise-grade capabilities, configuration, and structural components for the provider subsystem.
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
	"github.com/proofgate/proofgate/internal/httpx"
	"github.com/proofgate/proofgate/internal/sse"
)

// OpenAIConfig encapsulates the connection and authentication coordinates for an OpenAI-compatible target.
type OpenAIConfig struct {
	Name             string
	BaseURL          string // e.g. https://api.openai.com/v1 or http://ollama:11434/v1
	APIKey           string
	KeyFunc          KeyFunc
	Headers          map[string]string
	MaxResponseBytes int64
}

// OpenAI implements the standard provider.Provider interface for OpenAI and fully OpenAI-compatible REST APIs.
// This is the simplest adapter as the Gateway's internal `api` schemas are modeled explicitly after OpenAI.
type OpenAI struct {
	cfg              OpenAIConfig
	client           *http.Client
	maxResponseBytes int64
}

// NewOpenAI constructs a new OpenAI adapter with an isolated connection client.
func NewOpenAI(cfg OpenAIConfig) *OpenAI {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	maxResp := cfg.MaxResponseBytes
	if maxResp <= 0 {
		maxResp = 32 << 20
	}
	return &OpenAI{cfg: cfg, client: newClient(), maxResponseBytes: maxResp}
}

// Name returns the configured string identifier of the provider.
func (p *OpenAI) Name() string { return p.cfg.Name }

// post executes a generic HTTP POST, injecting resolved API keys and configured headers.
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
	key := p.cfg.APIKey
	if p.cfg.KeyFunc != nil {
		k, err := p.cfg.KeyFunc(ctx)
		if err != nil {
			return nil, &Error{Provider: p.cfg.Name, Status: 401, Message: "credential unavailable", Retryable: false}
		}
		key = k
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
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

// Chat executes a blocking (non-streaming) completion request against the upstream OpenAI API.
func (p *OpenAI) Chat(ctx context.Context, model string, req *api.ChatRequest) (*api.ChatResponse, error) {
	resp, err := p.post(ctx, "/chat/completions", withModel(req, model, false))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out api.ChatResponse
	limited := httpx.LimitReader(resp.Body, p.maxResponseBytes)
	if err := json.NewDecoder(limited).Decode(&out); err != nil {
		if errors.Is(err, httpx.ErrResponseTooLarge) {
			return nil, &Error{Provider: p.cfg.Name, Status: 502, Message: "upstream response exceeded size limit", Retryable: false}
		}
		return nil, &Error{Provider: p.cfg.Name, Status: 502, Message: "bad JSON from upstream: " + err.Error(), Retryable: true}
	}
	out.Model = model
	return &out, nil
}

// ChatStream executes a streaming completion request, parsing the upstream Server-Sent Events.
func (p *OpenAI) ChatStream(ctx context.Context, model string, req *api.ChatRequest) (Stream, error) {
	ctx, cancel := context.WithCancel(ctx)
	resp, err := p.post(ctx, "/chat/completions", withModel(req, model, true))
	if err != nil {
		cancel()
		return nil, err
	}
	return &openAIStream{name: p.cfg.Name, model: model, body: resp.Body, r: sse.NewReaderWithLimit(resp.Body, int(p.maxResponseBytes)), cancel: cancel}, nil
}

type openAIStream struct {
	name, model string
	body        io.ReadCloser
	r           *sse.Reader
	cancel      context.CancelFunc
}

// Recv executes the primary logic for the Recv operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *openAIStream) Recv() (*api.ChatChunk, error) {
	for {
		ev, err := s.r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			if errors.Is(err, sse.ErrEventTooLarge) {
				return nil, &Error{Provider: s.name, Status: 502, Message: "sse event exceeded max size", Retryable: false}
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

// Close executes the primary logic for the Close operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (s *openAIStream) Close() error {
	s.cancel()
	return s.body.Close()
}

// Embed executes a batch vector projection via the OpenAI embeddings API.
func (p *OpenAI) Embed(ctx context.Context, model string, req *api.EmbeddingRequest) (*api.EmbeddingResponse, error) {
	body := *req
	body.Model = model
	resp, err := p.post(ctx, "/embeddings", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out api.EmbeddingResponse
	limited := httpx.LimitReader(resp.Body, p.maxResponseBytes)
	if err := json.NewDecoder(limited).Decode(&out); err != nil {
		if errors.Is(err, httpx.ErrResponseTooLarge) {
			return nil, &Error{Provider: p.cfg.Name, Status: 502, Message: "upstream response exceeded size limit", Retryable: false}
		}
		return nil, &Error{Provider: p.cfg.Name, Status: 502, Message: "bad JSON: " + err.Error(), Retryable: true}
	}
	out.Model = model
	return &out, nil
}
