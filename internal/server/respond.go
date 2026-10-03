// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/provider"
)

// usd transforms a cost measured in millionths of a cent (micros)
// into a standardized string representation of US Dollars.
func usd(micros int64) string { return fmt.Sprintf("%.6f", float64(micros)/1e6) }

// writeCallHeaders injects custom ProofGate trace headers into the outgoing HTTP response.
// These headers provide visibility to clients regarding caching behavior, load balancing
// (which upstream provider actually served the prompt), and retry telemetry.
// This function must strictly be executed BEFORE the HTTP status line (w.WriteHeader) is written,
// otherwise the net/http library will finalize the header block and these writes will fail.
func writeCallHeaders(w http.ResponseWriter, c *pipeline.Call) {
	h := w.Header()
	// Flush any custom headers mutated by pipeline plugins
	for k, vs := range c.Header {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	// Append foundational gateway telemetry
	h.Set("X-ProofGate-Request-Id", c.ID)
	if c.Route != nil {
		h.Set("X-ProofGate-Route", c.Route.Name)
	}
	if c.Target.Provider != "" {
		h.Set("X-ProofGate-Target", c.Target.String())
	}
	h.Set("X-ProofGate-Attempts", strconv.Itoa(c.Attempts))
	h.Set("X-ProofGate-Cache", c.CacheStatus)
}

// writeJSON encapsulates the boilerplate required to emit a JSON response:
// serialization, explicit Content-Type tagging, and status code injection.
// If the struct fails to serialize (e.g., unsupported channel data), it falls back to a 500.
func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// upstreamError acts as an error coalescing middleware.
// It translates deeply-nested domain errors (like context timeouts or provider-specific
// SDK errors) into strictly typed `api.Error` structs that conform to the OpenAI API standard.
// This ensures that clients receive structured JSON payloads (like 429 RateLimit or 504 Timeout)
// rather than raw generic TCP error strings.
// Returns nil if the error originates from the client intentionally dropping the connection.
func upstreamError(err error) error {
	if err == nil {
		return nil
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	if errors.Is(err, context.Canceled) {
		// Client hung up gracefully
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// The internal gateway deadline elapsed before the provider responded
		return api.GatewayTimeout("upstream timed out")
	}

	var pe *provider.Error
	if errors.As(err, &pe) {
		var outErr *api.Error
		switch pe.Status {
		case 400, 404, 413, 422:
			outErr = &api.Error{Status: pe.Status, Message: pe.Message, Type: "invalid_request_error", Code: "upstream_rejected"}
		case 429:
			outErr = &api.Error{Status: 429, Message: pe.Message, Type: "rate_limit_error", Code: "rate_limit_exceeded"}
		default:
			// If all fallback targets in a route fail, bubble up a generic 502 Bad Gateway
			outErr = api.Upstream(fmt.Sprintf("all targets failed; last: %s %d", pe.Provider, pe.Status))
		}
		if pe.RetryAfter > 0 {
			outErr.RetryAfter = pe.RetryAfter
		}
		return outErr
	}
	return api.Upstream("all targets failed")
}

// decodeJSON securely unmarshals an incoming HTTP JSON payload into the target interface `dst`.
// Security properties enforced:
// 1. Strict Size Bounds: Capped at 64KB via `http.MaxBytesReader` to prevent large-payload DoS vectors.
// 2. Strict Schema validation: Rejects payloads with unknown/unmapped JSON fields to prevent schema injection.
// Any violation emits the corresponding standard HTTP error directly to the response writer.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
				"error":   "payload_too_large",
				"message": "request body exceeds 64KB limit",
			})
			return err
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "bad_request",
			"message": "invalid request body",
		})
		return err
	}
	return nil
}
