package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

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
	return r
}
