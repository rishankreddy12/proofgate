// Package server provides the HTTP presentation and request-handling layer of the ProofGate application.
// It bridges the gap between external REST clients and internal domain services (like the pipeline,
// routing, and cache subsystems) and manages request lifecycles, streaming, and HTTP middleware.
package server

import (
	"encoding/json"
	"net/http"

	"github.com/proofgate/proofgate/internal/cache"
	"github.com/redis/go-redis/v9"
)

// AdminCachePurgeHandler constructs an http.HandlerFunc that processes POST requests
// to securely invalidate semantic or exact cache entries from the admin control plane.
// It relies on a pre-authenticated context and directly commands the Redis cluster
// to evict matching keys based on the provided PurgeOptions payload.
func AdminCachePurgeHandler(rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var opts cache.PurgeOptions
		if r.Body != nil && r.ContentLength != 0 {
			if err := decodeJSON(w, r, &opts); err != nil {
				// decodeJSON handles writing the 400 response on failure.
				return
			}
		}

		// Execute the distributed eviction via the core cache package.
		res, err := cache.Purge(r.Context(), rdb, opts)
		if err != nil {
			http.Error(w, "purge failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}
