// Package cache provides enterprise-grade capabilities, configuration, and structural components for the cache subsystem.
package cache

import (
	"time"

	"github.com/proofgate/proofgate/internal/api"
)

// Entry represents a single cached Chat Completion payload serialized into Redis.
// It is used uniformly for both Exact and Semantic caching strategies.
type Entry struct {
	SourceRequestID string            `json:"source"`
	Query           string            `json:"query,omitempty"` // Semantic text used to generate the embedding (empty for exact-only)
	Response        *api.ChatResponse `json:"response"`
	CostMicros      int64             `json:"cost_micros"`
	CreatedAt       time.Time         `json:"created_at"`
	Tags            []string          `json:"tags,omitempty"`
	ContextHash     string            `json:"context_hash,omitempty"`
}

// Match encapsulates a resolved semantic cache hit returned by RediSearch.
type Match struct {
	Entry      Entry   // The deserialized cached completion
	Similarity float64 // Cosine similarity score [0.0, 1.0]
	Key        string  // The underlying Redis document key
}

// tenantTag generates a Redis hash tag (e.g. `{t:tenant-123}`).
// This ensures that all cache keys and tag sets for a specific tenant map to the
// same hash slot in a Redis Cluster environment, allowing atomic MULTI/EXEC pipelines.
func tenantTag(tenantID string) string { return "{t:" + tenantID + "}" }

// ExactKey constructs the deterministic Redis key for an Exact Cache entry.
func ExactKey(tenantID, route, hash string) string {
	return "cache:" + tenantTag(tenantID) + ":x:" + route + ":" + hash
}

// TagKey constructs the Redis Set key used for tracking and purging cached entries by tag.
func TagKey(tenantID, tag string) string { return "cachetag:" + tenantTag(tenantID) + ":" + tag }
