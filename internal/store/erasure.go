package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// TenantEraseComponent is the receipt for one independently operated store.
// ClickHouse reports submitted because ALTER TABLE DELETE is asynchronous.
type TenantEraseComponent struct {
	Status  string `json:"status"`
	Deleted int64  `json:"deleted,omitempty"`
	Error   string `json:"error,omitempty"`
	Pending bool   `json:"pending,omitempty"`
}

// TenantEraseReceipt is safe to return to an administrator: it contains only
// tenant identifiers, counts, statuses, and a correlation ID. A status of
// accepted means all requested operations were accepted, with ClickHouse
// mutation completion still pending; partial means at least one store failed.
type TenantEraseReceipt struct {
	ID         string                          `json:"id"`
	TenantID   string                          `json:"tenant_id"`
	Status     string                          `json:"status"`
	StartedAt  time.Time                       `json:"started_at"`
	FinishedAt time.Time                       `json:"finished_at"`
	Pending    bool                            `json:"pending"`
	Components map[string]TenantEraseComponent `json:"components"`
}

// TenantPostgresEraser is the database portion of tenant erasure.
type TenantPostgresEraser interface {
	EraseTenantRecords(context.Context, string) (TenantPostgresEraseResult, error)
}

// TenantRedisEraser is the Redis portion of tenant erasure.
type TenantRedisEraser interface {
	PurgeTenant(context.Context, string) (int64, error)
}

// TenantAnalyticsEraser submits the analytics mutation for a tenant.
type TenantAnalyticsEraser interface {
	PurgeTenantData(context.Context, string) error
}

// TenantPostgresEraseResult reports rows removed in one transaction.
type TenantPostgresEraseResult struct {
	APIKeys             int64
	ProviderCredentials int64
	Tenant              int64
}

// TenantEraser coordinates independent stores without hiding partial failure.
// It always attempts every configured component, so a temporary outage in one
// backend does not prevent safe, idempotent cleanup in the others.
type TenantEraser struct {
	postgres  TenantPostgresEraser
	redis     TenantRedisEraser
	analytics TenantAnalyticsEraser
}

// NewTenantEraser wires the production PostgreSQL, Redis, and analytics
// implementations. A nil dependency is deliberately reported as unavailable
// by Erase rather than treated as an empty successful store.
func NewTenantEraser(pg *Store, rdb redis.UniversalClient, analytics TenantAnalyticsEraser) *TenantEraser {
	e := &TenantEraser{postgres: pg, analytics: analytics}
	if rdb != nil {
		e.redis = redisTenantEraser{rdb: rdb}
	}
	return e
}

// NewTenantEraserWithStores is useful for deterministic unit tests and for
// applications that wrap their store clients with observability.
func NewTenantEraserWithStores(pg TenantPostgresEraser, r TenantRedisEraser, a TenantAnalyticsEraser) *TenantEraser {
	return &TenantEraser{postgres: pg, redis: r, analytics: a}
}

// Erase executes tenant erasure idempotently and returns a receipt even when
// one or more components fail. Invalid tenant IDs are rejected before any
// backend is touched.
func (e *TenantEraser) Erase(ctx context.Context, tenantID string) TenantEraseReceipt {
	started := time.Now().UTC()
	receipt := TenantEraseReceipt{
		ID:         uuid.NewString(),
		TenantID:   strings.TrimSpace(tenantID),
		Status:     "partial",
		StartedAt:  started,
		Components: make(map[string]TenantEraseComponent, 3),
	}
	if receipt.TenantID == "" {
		receipt.Components["postgres"] = TenantEraseComponent{Status: "failed", Error: "tenant id required"}
		receipt.Components["redis"] = TenantEraseComponent{Status: "skipped", Error: "tenant id required"}
		receipt.Components["clickhouse"] = TenantEraseComponent{Status: "skipped", Error: "tenant id required"}
		receipt.FinishedAt = time.Now().UTC()
		return receipt
	}

	if e == nil || e.postgres == nil {
		receipt.Components["postgres"] = TenantEraseComponent{Status: "unavailable", Error: "postgres store not configured"}
	} else {
		result, err := e.postgres.EraseTenantRecords(ctx, receipt.TenantID)
		if err != nil {
			receipt.Components["postgres"] = TenantEraseComponent{Status: "failed", Error: err.Error()}
		} else {
			receipt.Components["postgres"] = TenantEraseComponent{Status: "completed", Deleted: result.APIKeys + result.ProviderCredentials + result.Tenant}
		}
	}

	if e == nil || e.redis == nil {
		receipt.Components["redis"] = TenantEraseComponent{Status: "unavailable", Error: "redis store not configured"}
	} else {
		deleted, err := e.redis.PurgeTenant(ctx, receipt.TenantID)
		if err != nil {
			receipt.Components["redis"] = TenantEraseComponent{Status: "failed", Error: err.Error()}
		} else {
			receipt.Components["redis"] = TenantEraseComponent{Status: "completed", Deleted: deleted}
		}
	}

	if e == nil || e.analytics == nil {
		receipt.Components["clickhouse"] = TenantEraseComponent{Status: "unavailable", Error: "clickhouse store not configured"}
	} else if err := e.analytics.PurgeTenantData(ctx, receipt.TenantID); err != nil {
		receipt.Components["clickhouse"] = TenantEraseComponent{Status: "failed", Error: err.Error()}
	} else {
		receipt.Components["clickhouse"] = TenantEraseComponent{Status: "submitted", Pending: true}
		receipt.Pending = true
	}

	receipt.Status = "accepted"
	for _, component := range receipt.Components {
		if component.Status == "failed" || component.Status == "unavailable" {
			receipt.Status = "partial"
			break
		}
	}
	receipt.FinishedAt = time.Now().UTC()
	return receipt
}

// EraseTenantRecords deletes tenant-owned PostgreSQL records in one
// transaction. API keys are explicitly deleted even though the schema also
// has ON DELETE CASCADE, making the ownership boundary visible and testable.
func (s *Store) EraseTenantRecords(ctx context.Context, tenantID string) (TenantPostgresEraseResult, error) {
	if strings.TrimSpace(tenantID) == "" {
		return TenantPostgresEraseResult{}, errors.New("tenant id required")
	}
	if s == nil || s.pool == nil {
		return TenantPostgresEraseResult{}, errors.New("postgres store not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TenantPostgresEraseResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var result TenantPostgresEraseResult
	if tag, err := tx.Exec(ctx, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID); err != nil {
		return TenantPostgresEraseResult{}, err
	} else {
		result.APIKeys = tag.RowsAffected()
	}
	// Tenant-specific provider credentials use the existing tenantID/provider
	// naming convention and intentionally have no plaintext in this query.
	if tag, err := tx.Exec(ctx, `DELETE FROM provider_credentials WHERE provider LIKE $1`, tenantID+"/%"); err != nil {
		return TenantPostgresEraseResult{}, err
	} else {
		result.ProviderCredentials = tag.RowsAffected()
	}
	if tag, err := tx.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID); err != nil {
		return TenantPostgresEraseResult{}, err
	} else {
		result.Tenant = tag.RowsAffected()
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantPostgresEraseResult{}, err
	}
	return result, nil
}

// TenantRedisPatterns documents every tenant-tagged Redis family currently
// used by cache, agent runs, fuzzy-loop history, budgets, and tenant session
// extensions. Admin sessions use usernames rather than tenant IDs and are not
// deleted by tenant erasure.
var TenantRedisPatterns = []string{
	"cache:{t:%s}:*",
	"cachetag:{t:%s}:*",
	"sc:*:{t:%s}:*",
	"run:{t:%s}:*",
	"runfp:{t:%s}:*",
	"runemb:{t:%s}:*",
	"budget:{t:%s}:*",
	"session:{t:%s}",
	"session:{t:%s}:*",
	"sessions:{t:%s}",
	"sessions:{t:%s}:*",
}

type redisTenantEraser struct{ rdb redis.UniversalClient }

func (r redisTenantEraser) PurgeTenant(ctx context.Context, tenantID string) (int64, error) {
	if r.rdb == nil {
		return 0, errors.New("redis store not configured")
	}
	seen := make(map[string]struct{})
	var deleted int64
	for _, pattern := range TenantRedisPatterns {
		cursor := uint64(0)
		for {
			keys, next, err := r.rdb.Scan(ctx, cursor, fmt.Sprintf(pattern, tenantID), 500).Result()
			if err != nil {
				return deleted, err
			}
			batch := make([]string, 0, len(keys))
			for _, key := range keys {
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				batch = append(batch, key)
			}
			if len(batch) > 0 {
				n, err := r.rdb.Unlink(ctx, batch...).Result()
				if err != nil {
					return deleted, err
				}
				deleted += n
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	return deleted, nil
}
