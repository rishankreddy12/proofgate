// Package provider adapts upstream LLM APIs to the api types. Adapters use net/http only.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/proofgate/proofgate/internal/api"
)

type KeyFunc func(ctx context.Context) (string, error)

type Provider interface {
	Name() string
	Chat(ctx context.Context, model string, req *api.ChatRequest) (*api.ChatResponse, error)
	ChatStream(ctx context.Context, model string, req *api.ChatRequest) (Stream, error)
	Embed(ctx context.Context, model string, req *api.EmbeddingRequest) (*api.EmbeddingResponse, error)
}

// Stream yields chunks until io.EOF. Close cancels the upstream request.
type Stream interface {
	Recv() (*api.ChatChunk, error)
	Close() error
}

// Error is an upstream failure. Status 0 means a network error. Retryable drives the router.
type Error struct {
	Provider  string
	Status    int
	Message   string
	Retryable bool
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %d %s", e.Provider, e.Status, e.Message) }

func RetryableStatus(status int) bool {
	switch status {
	case 408, 409, 429, 500, 502, 503, 504, 529:
		return true
	}
	return false
}

// FromHTTP builds an Error from a non-2xx response and closes its body.
func FromHTTP(provider string, resp *http.Response) *Error {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	msg := http.StatusText(resp.StatusCode)
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &parsed) == nil {
		switch {
		case parsed.Error.Message != "":
			msg = parsed.Error.Message
		case parsed.Message != "":
			msg = parsed.Message
		}
	}
	return &Error{Provider: provider, Status: resp.StatusCode, Message: msg, Retryable: RetryableStatus(resp.StatusCode)}
}

// netError marks transport failures (DNS, reset, timeout) as retryable, unless the caller cancelled.
func netError(ctx context.Context, provider string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return &Error{Provider: provider, Message: err.Error(), Retryable: true}
}
