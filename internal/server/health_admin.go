package server

import "net/http"

func (h *Handlers) HealthAdmin(w http.ResponseWriter, _ *http.Request) {
	if h.Health == nil {
		writeJSON(w, 200, map[string]any{})
		return
	}
	writeJSON(w, 200, h.Health.Snapshot())
}
