package store

import (
	"context"
	"encoding/json"
	"time"
)

type Override struct {
	Route, Key, Value, Reason, Actor string
	UpdatedAt                        time.Time
}

type ProofEvent struct {
	ID     int64
	At     time.Time
	Kind   string
	Route  string
	Detail json.RawMessage
	Actor  string
}

func (s *Store) SetOverride(ctx context.Context, o Override) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `INSERT INTO route_overrides(route, key, value, reason, actor) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (route, key) DO UPDATE SET value=EXCLUDED.value, reason=EXCLUDED.reason, actor=EXCLUDED.actor, updated_at=now()`,
		o.Route, o.Key, o.Value, o.Reason, o.Actor); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]string{"key": o.Key, "value": o.Value, "reason": o.Reason})
	if _, err := tx.Exec(ctx, `INSERT INTO proof_events(kind, route, detail, actor) VALUES ('override',$1,$2,$3)`, o.Route, detail, o.Actor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ClearOverride(ctx context.Context, route, key, actor, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM route_overrides WHERE route=$1 AND key=$2`, route, key); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]string{"key": key, "reason": reason})
	if _, err := tx.Exec(ctx, `INSERT INTO proof_events(kind, route, detail, actor) VALUES ('override_cleared',$1,$2,$3)`, route, detail, actor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Overrides(ctx context.Context) ([]Override, error) {
	rows, err := s.pool.Query(ctx, `SELECT route, key, value, reason, actor, updated_at FROM route_overrides ORDER BY route, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Override
	for rows.Next() {
		var o Override
		if err := rows.Scan(&o.Route, &o.Key, &o.Value, &o.Reason, &o.Actor, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) AddProofEvent(ctx context.Context, kind, route string, detail any, actor string) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO proof_events(kind, route, detail, actor) VALUES ($1,$2,$3,$4)`, kind, route, b, actor)
	return err
}

func (s *Store) ProofEvents(ctx context.Context, route string, limit int) ([]ProofEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, at, kind, route, detail, actor FROM proof_events
		WHERE route=$1 ORDER BY at DESC, id DESC LIMIT $2`, route, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProofEvent
	for rows.Next() {
		var e ProofEvent
		if err := rows.Scan(&e.ID, &e.At, &e.Kind, &e.Route, &e.Detail, &e.Actor); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
