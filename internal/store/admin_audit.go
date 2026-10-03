// Package store provides enterprise-grade capabilities, configuration, and structural components for the store subsystem.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AdminAuditEvent represents a single, immutable action taken by a gateway administrator.
// It is used for compliance, forensic analysis, and rollback auditing.
type AdminAuditEvent struct {
	ID     int64           `json:"id"`
	TS     time.Time       `json:"ts"`
	Actor  string          `json:"actor"`
	Action string          `json:"action"`
	Target string          `json:"target"`
	Detail json.RawMessage `json:"detail"`
	IP     string          `json:"ip"`
	Result string          `json:"result"`
}

// AdminAuditFilter defines the query parameters for filtering the admin audit log.
type AdminAuditFilter struct {
	Since  time.Time
	Actor  string
	Action string
	Limit  int
}

// RecordAdminAudit synchronously writes an audit event to the database.
func (s *Store) RecordAdminAudit(ctx context.Context, actor, action, target string, detail any, ip, result string) error {
	if result == "" {
		result = "ok"
	}
	var raw json.RawMessage = []byte("{}")
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			raw = b
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO admin_audit(actor, action, target, detail, ip, result)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, actor, action, target, raw, ip, result)
	return err
}

// ListAdminAudit retrieves the most recent audit events up to the specified limit.
func (s *Store) ListAdminAudit(ctx context.Context, limit int) ([]AdminAuditEvent, error) {
	return s.ListAdminAuditFiltered(ctx, AdminAuditFilter{Limit: limit})
}

// ListAdminAuditFiltered retrieves audit events matching the provided AdminAuditFilter.
func (s *Store) ListAdminAuditFiltered(ctx context.Context, f AdminAuditFilter) ([]AdminAuditEvent, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}

	query := "SELECT id, ts, actor, action, target, detail, ip, result FROM admin_audit WHERE 1=1"
	var args []any
	argIdx := 1

	if !f.Since.IsZero() {
		query += fmt.Sprintf(" AND ts >= $%d", argIdx)
		args = append(args, f.Since)
		argIdx++
	}
	if f.Actor != "" {
		query += fmt.Sprintf(" AND actor = $%d", argIdx)
		args = append(args, f.Actor)
		argIdx++
	}
	if f.Action != "" {
		query += fmt.Sprintf(" AND action = $%d", argIdx)
		args = append(args, f.Action)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY ts DESC LIMIT $%d", argIdx)
	args = append(args, f.Limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []AdminAuditEvent
	for rows.Next() {
		var e AdminAuditEvent
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &e.Target, &e.Detail, &e.IP, &e.Result); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
