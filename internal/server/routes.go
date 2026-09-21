package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/go-chi/chi/v5"
	"github.com/proofgate/proofgate/internal/api"
)

// Recover converts panics into a generic 500. It logs the panic, never the request.
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

// Routes returns the public API. authMW authenticates /v1/*; /healthz is open.
func (h *Handlers) Routes(authMW func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Post("/v1/chat/completions", h.Chat)
		r.Post("/v1/embeddings", h.Embeddings)
		r.Get("/v1/models", h.Models)
		if h.MCP != nil {
			r.Handle("/mcp/{server}", h.MCP)
		}
	})
	return Recover(r)
}
