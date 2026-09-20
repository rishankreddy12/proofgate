package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Error is a client-facing error in the OpenAI shape.
type Error struct {
	Status     int           `json:"-"`
	Message    string        `json:"message"`
	Type       string        `json:"type"`
	Code       string        `json:"code,omitempty"`
	RetryAfter time.Duration `json:"-"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func BadRequest(msg string) *Error {
	return &Error{Status: 400, Message: msg, Type: "invalid_request_error", Code: "invalid_request"}
}
func Unauthorized() *Error {
	return &Error{Status: 401, Message: "invalid or missing API key", Type: "authentication_error", Code: "unauthorized"}
}
func Forbidden(code, msg string) *Error {
	return &Error{Status: 403, Message: msg, Type: "permission_error", Code: code}
}
func RateLimited(code string, retryAfter time.Duration) *Error {
	msg := "rate limit exceeded"
	if code == "request_too_large" {
		return &Error{Status: 413, Message: "request needs more tokens than the per-minute limit", Type: "invalid_request_error", Code: code}
	}
	return &Error{Status: 429, Message: msg, Type: "rate_limit_error", Code: code, RetryAfter: retryAfter}
}
func BudgetExceeded(msg string) *Error {
	return &Error{Status: 402, Message: msg, Type: "insufficient_quota", Code: "budget_exceeded"}
}
func Upstream(msg string) *Error {
	return &Error{Status: 502, Message: msg, Type: "api_error", Code: "upstream_error"}
}
func NoHealthyTarget() *Error {
	return &Error{Status: 503, Message: "no healthy target for this route", Type: "api_error", Code: "no_healthy_target"}
}
func Internal() *Error {
	return &Error{Status: 500, Message: "internal error", Type: "api_error", Code: "internal_error"}
}

// WriteError writes err as JSON. Unknown errors become a generic 500 so internals never leak.
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
