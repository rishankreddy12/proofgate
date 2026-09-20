package cache

import (
	"time"

	"github.com/proofgate/proofgate/internal/api"
)

// Entry is one cached response. Query is the semantic text it was stored under (empty for exact-only).
type Entry struct {
	SourceRequestID string            `json:"source"`
	Query           string            `json:"query,omitempty"`
	Response        *api.ChatResponse `json:"response"`
	CostMicros      int64             `json:"cost_micros"`
	CreatedAt       time.Time         `json:"created_at"`
	Tags            []string          `json:"tags,omitempty"`
}

type Match struct {
	Entry      Entry
	Similarity float64
	Key        string
}

func tenantTag(tenantID string) string { return "{t:" + tenantID + "}" }

func ExactKey(tenantID, route, hash string) string {
	return "cache:" + tenantTag(tenantID) + ":x:" + route + ":" + hash
}

func TagKey(tenantID, tag string) string { return "cachetag:" + tenantTag(tenantID) + ":" + tag }
