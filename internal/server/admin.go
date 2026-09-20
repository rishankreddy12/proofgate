package server

import (
	"encoding/json"
	"net/http"

	"github.com/proofgate/proofgate/internal/cache"
	"github.com/redis/go-redis/v9"
)

// AdminCachePurgeHandler handles POST /admin/cache/purge requests.
func AdminCachePurgeHandler(rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var opts cache.PurgeOptions
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
				http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		res, err := cache.Purge(r.Context(), rdb, opts)
		if err != nil {
			http.Error(w, "purge failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}
