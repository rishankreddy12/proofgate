package server

import (
	"net/http"

	"github.com/proofgate/proofgate/internal/auth"
)

type modelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

func (h *Handlers) Models(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	data := []modelEntry{}
	for _, rt := range h.State.Load().Router.Routes() {
		if p.CanUseRoute(rt.Name) {
			data = append(data, modelEntry{ID: rt.Name, Object: "model", OwnedBy: "proofgate"})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}
