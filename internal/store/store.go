// Package store is the Postgres repository for tenants, keys and (later) overrides and credentials.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Tenant struct {
	ID        string
	Name      string
	Policy    TenantPolicy
	CreatedAt time.Time
}

type APIKey struct {
	ID            string
	TenantID      string
	Name          string
	Prefix        string
	AllowedRoutes []string
	Policy        KeyPolicy
	CreatedAt     time.Time
	RevokedAt     *time.Time
}

type KeyRecord struct {
	Key    APIKey
	Tenant Tenant
}

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
func (s *Store) Pool() *pgxpool.Pool            { return s.pool }

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *Store) CreateTenant(ctx context.Context, name string, p TenantPolicy) (Tenant, error) {
	t := Tenant{Name: name, Policy: p}
	err := s.pool.QueryRow(ctx, `INSERT INTO tenants(name, policy) VALUES ($1, $2) RETURNING id::text, created_at`, name, p).
		Scan(&t.ID, &t.CreatedAt)
	return t, err
}

func (s *Store) TenantByName(ctx context.Context, name string) (Tenant, error) {
	var t Tenant
	err := s.pool.QueryRow(ctx, `SELECT id::text, name, policy, created_at FROM tenants WHERE name=$1`, name).
		Scan(&t.ID, &t.Name, &t.Policy, &t.CreatedAt)
	return t, notFound(err)
}

func (s *Store) UpdateTenantPolicy(ctx context.Context, id string, p TenantPolicy) error {
	tag, err := s.pool.Exec(ctx, `UPDATE tenants SET policy=$2 WHERE id=$1`, id, p)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) CreateKey(ctx context.Context, tenantID, name, prefix string, hash []byte, routes []string, p KeyPolicy) (APIKey, error) {
	if routes == nil {
		routes = []string{}
	}
	k := APIKey{TenantID: tenantID, Name: name, Prefix: prefix, AllowedRoutes: routes, Policy: p}
	err := s.pool.QueryRow(ctx, `INSERT INTO api_keys(tenant_id, name, prefix, hash, allowed_routes, policy)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text, created_at`, tenantID, name, prefix, hash, routes, p).
		Scan(&k.ID, &k.CreatedAt)
	return k, err
}

func (s *Store) KeyByHash(ctx context.Context, hash []byte) (KeyRecord, error) {
	var r KeyRecord
	err := s.pool.QueryRow(ctx, `
		SELECT k.id::text, k.tenant_id::text, k.name, k.prefix, k.allowed_routes, k.policy, k.created_at,
		       t.id::text, t.name, t.policy, t.created_at
		FROM api_keys k JOIN tenants t ON t.id = k.tenant_id
		WHERE k.hash = $1 AND k.revoked_at IS NULL`, hash).
		Scan(&r.Key.ID, &r.Key.TenantID, &r.Key.Name, &r.Key.Prefix, &r.Key.AllowedRoutes, &r.Key.Policy, &r.Key.CreatedAt,
			&r.Tenant.ID, &r.Tenant.Name, &r.Tenant.Policy, &r.Tenant.CreatedAt)
	return r, notFound(err)
}

func (s *Store) RevokeKey(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ListKeys(ctx context.Context, tenantID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, tenant_id::text, name, prefix, allowed_routes, policy, created_at, revoked_at
		FROM api_keys WHERE tenant_id=$1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.TenantID, &k.Name, &k.Prefix, &k.AllowedRoutes, &k.Policy, &k.CreatedAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
