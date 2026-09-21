package store

import (
	"context"
	"time"

	"github.com/proofgate/proofgate/internal/secrets"
)

type CredentialInfo struct {
	Provider  string
	Version   int
	KEKID     string
	CreatedAt time.Time
	CreatedBy string
	Active    bool
}

func (s *Store) PutCredential(ctx context.Context, provider string, sealed secrets.Sealed, actor string) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "cred:"+provider); err != nil {
		return 0, err
	}
	var v int
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(version), 0) + 1 FROM provider_credentials WHERE provider=$1`, provider).Scan(&v); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE provider_credentials SET active=false WHERE provider=$1 AND active`, provider); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO provider_credentials(provider, version, kek_id, wrapped_dek, nonce, ciphertext, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, provider, v, sealed.KEKID, sealed.WrappedDEK, sealed.Nonce, sealed.Ciphertext, actor); err != nil {
		return 0, err
	}
	return v, tx.Commit(ctx)
}

func (s *Store) ActiveCredential(ctx context.Context, provider string) (secrets.Sealed, int, error) {
	var out secrets.Sealed
	var v int
	err := s.pool.QueryRow(ctx, `SELECT version, kek_id, wrapped_dek, nonce, ciphertext FROM provider_credentials
		WHERE provider=$1 AND active`, provider).Scan(&v, &out.KEKID, &out.WrappedDEK, &out.Nonce, &out.Ciphertext)
	return out, v, notFound(err)
}

func (s *Store) ListCredentials(ctx context.Context) ([]CredentialInfo, error) {
	rows, err := s.pool.Query(ctx, `SELECT provider, version, kek_id, created_at, created_by, active
		FROM provider_credentials ORDER BY provider, version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CredentialInfo
	for rows.Next() {
		var c CredentialInfo
		if err := rows.Scan(&c.Provider, &c.Version, &c.KEKID, &c.CreatedAt, &c.CreatedBy, &c.Active); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ReplaceWrapped(ctx context.Context, provider string, version int, sealed secrets.Sealed) error {
	_, err := s.pool.Exec(ctx, `UPDATE provider_credentials SET kek_id=$3, wrapped_dek=$4 WHERE provider=$1 AND version=$2`,
		provider, version, sealed.KEKID, sealed.WrappedDEK)
	return err
}

// AllSealed returns every record (for KEK rotation).
func (s *Store) AllSealed(ctx context.Context) (map[[2]any]secrets.Sealed, error) {
	rows, err := s.pool.Query(ctx, `SELECT provider, version, kek_id, wrapped_dek, nonce, ciphertext FROM provider_credentials`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[[2]any]secrets.Sealed{}
	for rows.Next() {
		var p string
		var v int
		var sl secrets.Sealed
		if err := rows.Scan(&p, &v, &sl.KEKID, &sl.WrappedDEK, &sl.Nonce, &sl.Ciphertext); err != nil {
			return nil, err
		}
		out[[2]any{p, v}] = sl
	}
	return out, rows.Err()
}
