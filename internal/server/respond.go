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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
		return &api.Error{Status: 504, Message: "upstream timed out", Type: "api_error", Code: "upstream_timeout"}
	}
	var pe *provider.Error
	if errors.As(err, &pe) {
		switch pe.Status {
		case 400, 404, 413, 422:
			return &api.Error{Status: pe.Status, Message: pe.Message, Type: "invalid_request_error", Code: "upstream_rejected"}
		}
		return api.Upstream(fmt.Sprintf("all targets failed; last: %s %d", pe.Provider, pe.Status))
	}
	return api.Upstream("all targets failed")
}
