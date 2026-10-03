// Package api provides enterprise-grade capabilities, configuration, and structural components for the api subsystem.
package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Error encapsulates a client-facing error structured identically to the standard
// OpenAI API error response format. This ensures that upstream clients (like the
// official OpenAI SDKs) can natively parse errors emitted by the LLM Gateway
// without requiring custom error handling logic on the client side.
type Error struct {
	// Status contains the HTTP status code (e.g., 400, 429, 500).
	// It is excluded from the JSON payload because it is transported in the HTTP header.
	Status int `json:"-"`

	// Message provides a human-readable description of the error condition.
	Message string `json:"message"`

	// Type categorizes the error (e.g., "invalid_request_error", "rate_limit_error").
	Type string `json:"type"`

	// Code represents a machine-readable string code for the specific error (e.g., "budget_exceeded").
	// This field is optional and omitted if empty.
	Code string `json:"code,omitempty"`

	// RetryAfter specifies an optional cool-down duration before the client should retry.
	// It is excluded from the JSON payload and instead serialized as a "Retry-After" HTTP header.
	RetryAfter time.Duration `json:"-"`
}

// Error implements the standard Go error interface.
// It returns a formatted string combining the error code and message.
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// BadRequest constructs a 400 error indicating client-side input validation failure.
func BadRequest(msg string) *Error {
	return &Error{Status: 400, Message: msg, Type: "invalid_request_error", Code: "invalid_request"}
}

// Unauthorized constructs a 401 error indicating missing or invalid authentication credentials.
func Unauthorized() *Error {
	return &Error{Status: 401, Message: "invalid or missing API key", Type: "authentication_error", Code: "unauthorized"}
}

// Forbidden constructs a 403 error indicating the authenticated client lacks permissions
// for the requested operation or model.
func Forbidden(code, msg string) *Error {
	return &Error{Status: 403, Message: msg, Type: "permission_error", Code: code}
}

// RateLimited constructs a 429 Too Many Requests error.
// If the payload itself exceeds the limits (request_too_large), it returns a 413 Payload Too Large.
// The provided retryAfter duration is automatically mapped to a Retry-After HTTP header.
func RateLimited(code string, retryAfter time.Duration) *Error {
	msg := "rate limit exceeded"
	if code == "request_too_large" {
		return &Error{Status: 413, Message: "request needs more tokens than the per-minute limit", Type: "invalid_request_error", Code: code}
	}
	return &Error{Status: 429, Message: msg, Type: "rate_limit_error", Code: code, RetryAfter: retryAfter}
}

// BudgetExceeded constructs a 402 Payment Required error.
// This is emitted when a tenant exhausts their allocated fiat or token budget.
func BudgetExceeded(msg string) *Error {
	return &Error{Status: 402, Message: msg, Type: "insufficient_quota", Code: "budget_exceeded"}
}

// Upstream constructs a 502 Bad Gateway error.
// This is emitted when the underlying target LLM provider returns an unexpected or invalid response.
func Upstream(msg string) *Error {
	return &Error{Status: 502, Message: msg, Type: "api_error", Code: "upstream_error"}
}

// NoHealthyTarget constructs a 503 Service Unavailable error.
// This is emitted by the router when all fallback providers for a specific route are degraded or exhausted.
func NoHealthyTarget() *Error {
	return &Error{Status: 503, Message: "no healthy target for this route", Type: "api_error", Code: "no_healthy_target"}
}

// GatewayTimeout constructs a 504 Gateway Timeout error.
// This is emitted when the upstream provider fails to respond within the configured deadline.
func GatewayTimeout(msg string) *Error {
	if msg == "" {
		msg = "gateway timeout"
	}
	return &Error{Status: 504, Message: msg, Type: "api_error", Code: "gateway_timeout"}
}

// Internal constructs a generic 500 Internal Server Error.
// Used as a fallback for untyped panics or unhandled system failures to prevent leaking internal state.
func Internal() *Error {
	return &Error{Status: 500, Message: "internal error", Type: "api_error", Code: "internal_error"}
}

// WriteError serializes the provided error to the HTTP response writer.
// It performs a type assertion: if the error is an *api.Error, it serializes it natively.
// If it is any other standard Go error, it is coerced into a generic 500 Internal error
// to guarantee that stack traces or sensitive internal paths are never leaked to external clients.
// It also handles injecting the Retry-After header if applicable.
func WriteError(w http.ResponseWriter, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = Internal()
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(e.RetryAfter.Seconds()))))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(map[string]*Error{"error": e})
}
