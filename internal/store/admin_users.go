package store

import (
	"context"
	"errors"
	"time"
)

var ErrLastAdmin = errors.New("cannot remove or disable the last enabled admin")

type AdminUser struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash []byte    `json:"-"`
	Role         string    `json:"role"`
	Enabled      bool      `json:"enabled"`
	MustChange   bool      `json:"must_change"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (s *Store) CreateAdminUser(ctx context.Context, username string, passwordHash []byte, role string) (AdminUser, error) {
	if role == "" {
		role = "admin"
	}
	u := AdminUser{
		Username:     username,
		PasswordHash: passwordHash,
		Role:         role,
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO admin_users(username, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id::text, enabled, must_change, created_at, updated_at
	`, username, passwordHash, role).Scan(&u.ID, &u.Enabled, &u.MustChange, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func (s *Store) GetAdminUser(ctx context.Context, username string) (AdminUser, error) {
	var u AdminUser
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, username, password_hash, role, enabled, must_change, created_at, updated_at
		FROM admin_users
		WHERE username = $1
	`, username).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Enabled, &u.MustChange, &u.CreatedAt, &u.UpdatedAt)
	return u, notFound(err)
}

func (s *Store) GetAdminUserByID(ctx context.Context, id string) (AdminUser, error) {
	var u AdminUser
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, username, password_hash, role, enabled, must_change, created_at, updated_at
		FROM admin_users
		WHERE id = $1
	`, id).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Enabled, &u.MustChange, &u.CreatedAt, &u.UpdatedAt)
	return u, notFound(err)
}

func (s *Store) ListAdminUsers(ctx context.Context) ([]AdminUser, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, username, role, enabled, must_change, created_at, updated_at
		FROM admin_users
		ORDER BY username ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AdminUser
	for rows.Next() {
		var u AdminUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.Enabled, &u.MustChange, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) UpdateAdminUserPassword(ctx context.Context, id string, passwordHash []byte, mustChange bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE admin_users
		SET password_hash = $2, must_change = $3, updated_at = now()
		WHERE id = $1
	`, id, passwordHash, mustChange)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) SetAdminUserEnabled(ctx context.Context, id string, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE admin_users
		SET enabled = $2, updated_at = now()
		WHERE id = $1
		  AND ($2 = true OR enabled = false OR role <> 'admin' OR EXISTS (
		    SELECT 1 FROM admin_users WHERE role = 'admin' AND enabled = true AND id <> $1
		  ))
	`, id, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if checkErr := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_users WHERE id = $1)`, id).Scan(&exists); checkErr == nil && !exists {
			return ErrNotFound
		}
		return ErrLastAdmin
	}
	return nil
}

func (s *Store) DeleteAdminUser(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM admin_users
		WHERE id = $1
		  AND (role <> 'admin' OR enabled = false OR EXISTS (
		    SELECT 1 FROM admin_users WHERE role = 'admin' AND enabled = true AND id <> $1
		  ))
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if checkErr := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_users WHERE id = $1)`, id).Scan(&exists); checkErr == nil && !exists {
			return ErrNotFound
		}
		return ErrLastAdmin
	}
	return nil
}

func (s *Store) AdminUserCount(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM admin_users`).Scan(&count)
	return count, err
}
