// Package telemetry provides Prometheus metrics, OpenTelemetry tracing, and access logs.
package telemetry

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/proofgate/proofgate/internal/auth"
)

// statusWriter wraps the standard http.ResponseWriter to transparently intercept
// and record the HTTP status code and response body size (in bytes).
// This is strictly used for telemetry and access logging without interfering
// with the actual byte stream sent to the client.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

// WriteHeader intercepts the HTTP status code, recording it locally before
// passing it down to the underlying ResponseWriter.
func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write intercepts the payload chunk, accumulating the total byte count
// before passing it down to the underlying ResponseWriter.
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Flush ensures that Server-Sent Events (SSE) and HTTP streaming continue
// to work seamlessly through the wrapper by delegating to the underlying flusher.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap provides access to the underlying http.ResponseWriter for middleware
// that requires direct socket access (e.g., hijackers).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// AccessLog is an HTTP middleware that emits a single structured JSON log line
// upon the completion of every incoming HTTP request.
//
// Security Note: It strictly logs metadata (method, path, status, latency) and
// ProofGate-specific trace headers. It explicitly NEVER logs raw HTTP headers,
// query parameters, or request/response bodies to ensure PII/PHI and credentials
// are never leaked into log aggregators (e.g., Datadog, Splunk).
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		var tenant string

		// Yield execution to the downstream pipeline
		next.ServeHTTP(sw, r)

		if p, ok := auth.FromContext(r.Context()); ok {
			tenant = p.TenantID
		}

		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"bytes", sw.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", sw.Header().Get("X-ProofGate-Request-Id"),
			"route", sw.Header().Get("X-ProofGate-Route"),
			"target", sw.Header().Get("X-ProofGate-Target"),
			"tenant", tenant)
	})
}
