package store

import (
	"context"
	"encoding/json"
	"time"
)

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

func (s *Store) ListAdminAudit(ctx context.Context, limit int) ([]AdminAuditEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, ts, actor, action, target, detail, ip, result
		FROM admin_audit
		ORDER BY ts DESC
		LIMIT $1
	`, limit)
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
