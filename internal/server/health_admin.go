// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
package server

import "net/http"

// HealthAdmin exposes the internal gossip health tracker state over HTTP.
// This endpoint is used by external infrastructure monitors and load balancers
// to inspect the cluster-wide consensus view of upstream provider health.
// It returns a JSON snapshot containing target statuses, RTT (latency) averages,
// and circuit breaker states.
func (h *Handlers) HealthAdmin(w http.ResponseWriter, _ *http.Request) {
	if h.Health == nil {
		// Degraded/Local mode where health tracking is disabled
		writeJSON(w, 200, map[string]any{})
		return
	}

	// Returns a point-in-time copy of the RWMutex protected internal state tree.
	writeJSON(w, 200, h.Health.Snapshot())
}
