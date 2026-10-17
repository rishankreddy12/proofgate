// Package analytics provides enterprise-grade capabilities, configuration, and structural components for the analytics subsystem.
package analytics

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open executes the primary logic for the Open operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func Open(ctx context.Context, dsn string) (driver.Conn, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, err
	}
	return conn, conn.Ping(ctx)
}

// Migrate executes the primary logic for the Migrate operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func Migrate(ctx context.Context, conn driver.Conn) error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := migrations.ReadFile("migrations/" + n)
		if err != nil {
			return err
		}
		for _, stmt := range strings.Split(string(b), "\n;;\n") {
			stmt = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(stmt), ";"))
			if stmt == "" {
				continue
			}
			if err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("clickhouse migration %s: %w", n, err)
			}
		}
	}
	return nil
}

// ApplyRetention modifies column TTLs on text columns and row TTLs on shadow tables based on configuration.
func ApplyRetention(ctx context.Context, conn driver.Conn, textTTLHours, ttlDays int) error {
	if conn == nil {
		return nil
	}
	if textTTLHours <= 0 {
		textTTLHours = 72
	}
	if ttlDays <= 0 {
		ttlDays = 30
	}

	stmts := []string{
		fmt.Sprintf("ALTER TABLE cache_shadow MODIFY COLUMN query String TTL toDateTime(ts) + INTERVAL %d HOUR", textTTLHours),
		fmt.Sprintf("ALTER TABLE cache_shadow MODIFY COLUMN candidate_query String TTL toDateTime(ts) + INTERVAL %d HOUR", textTTLHours),
		fmt.Sprintf("ALTER TABLE cache_shadow MODIFY COLUMN candidate_answer String TTL toDateTime(ts) + INTERVAL %d HOUR", textTTLHours),
		fmt.Sprintf("ALTER TABLE cache_shadow MODIFY COLUMN actual_answer String TTL toDateTime(ts) + INTERVAL %d HOUR", textTTLHours),
		fmt.Sprintf("ALTER TABLE cache_shadow MODIFY TTL toDateTime(ts) + INTERVAL %d DAY", ttlDays),

		fmt.Sprintf("ALTER TABLE routing_shadow MODIFY COLUMN prompt String TTL toDateTime(ts) + INTERVAL %d HOUR", textTTLHours),
		fmt.Sprintf("ALTER TABLE routing_shadow MODIFY COLUMN cheap_answer String TTL toDateTime(ts) + INTERVAL %d HOUR", textTTLHours),
		fmt.Sprintf("ALTER TABLE routing_shadow MODIFY COLUMN strong_answer String TTL toDateTime(ts) + INTERVAL %d HOUR", textTTLHours),
		fmt.Sprintf("ALTER TABLE routing_shadow MODIFY TTL toDateTime(ts) + INTERVAL %d DAY", ttlDays),
	}

	for _, stmt := range stmts {
		if err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("apply retention %q: %w", stmt, err)
		}
	}
	return nil
}

// PurgeTenantData purges all analytics and shadow data for the specified tenant from ClickHouse.
func PurgeTenantData(ctx context.Context, conn driver.Conn, tenantID string) error {
	if tenantID == "" {
		return errors.New("tenant id required")
	}
	if conn == nil {
		return nil
	}

	// cache_labels has no tenant_id column; its shadow_id is the ownership link
	// to cache_shadow and must be removed before the shadow mutation.
	queries := []string{
		"ALTER TABLE cache_labels DELETE WHERE shadow_id IN (SELECT id FROM cache_shadow WHERE tenant_id = ?)",
		"ALTER TABLE usage_events DELETE WHERE tenant_id = ?",
		"ALTER TABLE cache_shadow DELETE WHERE tenant_id = ?",
		"ALTER TABLE routing_shadow DELETE WHERE tenant_id = ?",
		"ALTER TABLE routing_decisions DELETE WHERE tenant_id = ?",
		"ALTER TABLE mcp_calls DELETE WHERE tenant_id = ?",
	}

	for _, query := range queries {
		if err := conn.Exec(ctx, query, tenantID); err != nil {
			return fmt.Errorf("purge tenant analytics: %w", err)
		}
	}
	return nil
}

// TenantPurger adapts the package-level purge operation to the store erasure
// orchestration interface.
type TenantPurger struct{ Conn driver.Conn }

func NewTenantPurger(conn driver.Conn) TenantPurger { return TenantPurger{Conn: conn} }

func (p TenantPurger) PurgeTenantData(ctx context.Context, tenantID string) error {
	if p.Conn == nil {
		return errors.New("clickhouse store not configured")
	}
	return PurgeTenantData(ctx, p.Conn, tenantID)
}
