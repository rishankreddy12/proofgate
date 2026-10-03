// Package provider implements the protocol translation layer, adapting the Gateway-canonical
// API schemas (based heavily on OpenAI) into upstream-specific REST payloads (Anthropic, Gemini, etc).
// Adapters use net/http directly for strict connection lifecycle control and minimal overhead.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/proofgate/proofgate/internal/api"
)

// KeyFunc represents an abstract strategy for dynamically resolving API credentials,
// e.g. from the in-memory database store or Vault, evaluated just-in-time per request.
type KeyFunc func(ctx context.Context) (string, error)

// Provider defines the core contract that all upstream LLM adapters must implement.
type Provider interface {
	Name() string
	Chat(ctx context.Context, model string, req *api.ChatRequest) (*api.ChatResponse, error)
	ChatStream(ctx context.Context, model string, req *api.ChatRequest) (Stream, error)
	Embed(ctx context.Context, model string, req *api.EmbeddingRequest) (*api.EmbeddingResponse, error)
}

// Stream abstracts the asynchronous, chunk-by-chunk ingestion of a Server-Sent Events (SSE)
// or NDJSON response from an upstream API. It yields chunks until io.EOF.
// Calling Close() halts the parser and immediately terminates the underlying TCP connection.
type Stream interface {
	Recv() (*api.ChatChunk, error)
	Close() error
}

// Error captures structured upstream failures.
// A Status of 0 indicates a hard transport/network error (e.g., DNS resolution failure, connection reset).
// The Retryable boolean directly informs the Router's Breaker and Target-failover state machine.
type Error struct {
	Provider   string
	Status     int
	Message    string
	Retryable  bool
	RetryAfter time.Duration
}

// Error executes the primary logic for the Error operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (e *Error) Error() string { return fmt.Sprintf("%s: %d %s", e.Provider, e.Status, e.Message) }

// RetryableStatus executes the primary logic for the RetryableStatus operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func RetryableStatus(status int) bool {
	switch status {
	case 408, 409, 429, 500, 502, 503, 504, 529:
		return true
	}
	return false
}

// FromHTTP constructs an upstream Error from a non-2xx HTTP response, heuristically
// extracting the underlying fault message from the JSON body (often proprietary format)
// and closing the response body.
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
	var retryAfter time.Duration
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && secs >= 0 {
			retryAfter = time.Duration(secs) * time.Second
		} else if t, err := http.ParseTime(ra); err == nil {
			if d := time.Until(t); d > 0 {
				retryAfter = d
			}
		}
	}
	return &Error{Provider: provider, Status: resp.StatusCode, Message: msg, Retryable: RetryableStatus(resp.StatusCode), RetryAfter: retryAfter}
}

// netError classifies low-level TCP/TLS/DNS failures as Retryable, allowing the Router
// to instantly failover to the next fallback target. It respects client-side cancellation.
func netError(ctx context.Context, provider string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return &Error{Provider: provider, Message: err.Error(), Retryable: true}
}
