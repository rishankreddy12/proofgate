//go:build integration

package store

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	pg, err := tcpg.Run(ctx, "postgres:16-alpine", tcpg.WithDatabase("pg"), tcpg.WithUsername("pg"), tcpg.WithPassword("pg"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	s, err := Open(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(s.Close)
	require.NoError(t, s.Migrate(ctx))
	require.NoError(t, s.Migrate(ctx), "migrations are idempotent")
	return s
}

func TestTenantsAndKeys(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ten, err := s.CreateTenant(ctx, "acme", TenantPolicy{RPM: 60, TPM: 10000, MonthlyBudgetUSD: 5})
	require.NoError(t, err)

	h := sha256.Sum256([]byte("pg_test_secret"))
	k, err := s.CreateKey(ctx, ten.ID, "ci", "pg_test_sec", h[:], []string{"default"}, KeyPolicy{AllowDirect: true})
	require.NoError(t, err)

	rec, err := s.KeyByHash(ctx, h[:])
	require.NoError(t, err)
	require.Equal(t, "acme", rec.Tenant.Name)
	require.Equal(t, 60, rec.Tenant.Policy.RPM)
	require.Equal(t, []string{"default"}, rec.Key.AllowedRoutes)
	require.True(t, rec.Key.Policy.AllowDirect)

	require.NoError(t, s.RevokeKey(ctx, k.ID))
	_, err = s.KeyByHash(ctx, h[:])
	require.ErrorIs(t, err, ErrNotFound)

	keys, err := s.ListKeys(ctx, ten.ID)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.NotNil(t, keys[0].RevokedAt)

	require.NoError(t, s.UpdateTenantPolicy(ctx, ten.ID, TenantPolicy{RPM: 1}))
	got, err := s.TenantByName(ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, 1, got.Policy.RPM)
}
