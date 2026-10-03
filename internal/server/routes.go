// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/go-chi/chi/v5"
	"github.com/proofgate/proofgate/internal/api"
)

// Recover is a global panic recovery middleware.
// It intercepts any unhandled nil pointer exceptions, array out-of-bounds, or explicit
// panics originating deep within the HTTP handler stack. It prevents the entire gateway
// process from crashing and instead swallows the panic, emitting a generic HTTP 500 error
// back to the client while logging the full stack trace to the system logger.
//
// Security Note: It strictly avoids logging the raw HTTP request body to prevent leaking
// sensitive PII/PHI or API keys present in the panic context.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic in handler", "panic", fmt.Sprint(v), "stack", string(debug.Stack()), "path", r.URL.Path)
				api.WriteError(w, api.Internal())
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Routes constructs the primary multiplexer (mux) for the public-facing Gateway API.
// It wires the foundational endpoints (Chat, Embeddings, Models) to their respective handlers
// and injects necessary middleware such as the panic Recoverer and the authentication enforcer.
//
// Endpoints under `/v1/*` strictly require the authMW (Authentication Middleware) to pass.
// Endpoints under `/healthz` are entirely unauthenticated to permit AWS ELB/Kubernetes health checks.
// If the Model Context Protocol (MCP) server integration is enabled, it mounts those proxy endpoints.
func (h *Handlers) Routes(authMW func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()

	// Unauthenticated health probe
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	// Authenticated OpenAI compatibility layer
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Post("/v1/chat/completions", h.Chat)
		r.Post("/v1/embeddings", h.Embeddings)
		r.Get("/v1/models", h.Models)

		// Conditionally mount the MCP pass-through proxy
		if h.MCP != nil {
			r.Handle("/mcp/{server}", h.MCP)
		}
	})

	// Wrap the entire tree in the panic recovery shield
	return Recover(r)
}
