// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
package server

import (
	"net/http"

	"github.com/proofgate/proofgate/internal/auth"
)

// modelEntry represents the OpenAI-compatible representation of an available Model.
// It maps directly to an element within the `data` array of a `/v1/models` response.
type modelEntry struct {
	// ID maps to the exact ProofGate route name that the user can pass into `model: "..."`.
	ID string `json:"id"`
	// Object must statically be "model" to comply with the standard SDK parsers.
	Object string `json:"object"`
	// OwnedBy statically designates ProofGate as the proxying authority.
	OwnedBy string `json:"owned_by"`
}

// Models is the HTTP handler for the `/v1/models` endpoint.
// It implements dynamic model discovery: It introspects the global router configuration
// and filters the list of exposed models based on the caller's RBAC capabilities
// (e.g. denying routes the tenant's API key is forbidden from accessing).
// This enables tools like litellm and continue.dev to seamlessly auto-complete available routes.
func (h *Handlers) Models(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())

	data := []modelEntry{}
	// Scan the router tree in the currently active immutable Runtime snapshot.
	for _, rt := range h.State.Load().Router.Routes() {
		if p.CanUseRoute(rt.Name) {
			data = append(data, modelEntry{ID: rt.Name, Object: "model", OwnedBy: "proofgate"})
		}
	}

	// Write the encapsulated list to adhere to the OpenAI envelope schema.
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}
