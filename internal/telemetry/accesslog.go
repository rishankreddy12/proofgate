package telemetry

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/proofgate/proofgate/internal/auth"
)

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Flush keeps SSE working through this wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// AccessLog writes one line per request. It logs no headers except the request id, and never bodies.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		var tenant string
		next.ServeHTTP(sw, r)
		if p, ok := auth.FromContext(r.Context()); ok {
			tenant = p.TenantID
		}
		slog.Info("request",
			"method", r.Method, "path", r.URL.Path, "status", sw.status, "bytes", sw.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", sw.Header().Get("X-ProofGate-Request-Id"),
			"route", sw.Header().Get("X-ProofGate-Route"),
			"target", sw.Header().Get("X-ProofGate-Target"),
			"tenant", tenant)
	})
}
