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

func usd(micros int64) string { return fmt.Sprintf("%.6f", float64(micros)/1e6) }

// writeCallHeaders sets ProofGate headers. It must run before the status line is written.
func writeCallHeaders(w http.ResponseWriter, c *pipeline.Call) {
	h := w.Header()
	for k, vs := range c.Header {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
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

// upstreamError maps router/provider errors to client-facing errors. It returns nil when the client left.
func upstreamError(err error) error {
	if err == nil {
		return nil
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
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
			outErr = api.Upstream(fmt.Sprintf("all targets failed; last: %s %d", pe.Provider, pe.Status))
		}
		if pe.RetryAfter > 0 {
			outErr.RetryAfter = pe.RetryAfter
		}
		return outErr
	}
	return api.Upstream("all targets failed")
}

// decodeJSON decodes a JSON request body into dst, strictly limiting the body to 64KB
// and disallowing unknown fields. If the body exceeds 64KB, it writes an HTTP 413
// Payload Too Large error response. If decoding fails for other reasons, it writes
// an HTTP 400 Bad Request error response.
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
