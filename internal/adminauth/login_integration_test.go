//go:build integration

package adminauth

import (
	"context"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func newRealStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	pg, err := tcpg.Run(ctx, "postgres:16-alpine",
		tcpg.WithDatabase("pg"),
		tcpg.WithUsername("pg"),
		tcpg.WithPassword("pg"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	s, err := store.Open(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(s.Close)

	require.NoError(t, s.Migrate(ctx))
	return s
}

func TestLoginIntegrationPostgresAndRedis(t *testing.T) {
	st := newRealStore(t)
	rdb := newRedisClient(t)
	ctx := context.Background()

	cfg := config.AdminAuthConfig{
		MaxLoginAttempts:   3,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 30 * time.Minute,
		SessionAbsTimeout:  12 * time.Hour,
	}
	svc := NewService(st, rdb, cfg, nil)

	// Create user in real PostgreSQL
	pw := "secureP@ssw0rd!"
	pwHash, err := HashPassword(pw)
	require.NoError(t, err)

	createdUser, err := st.CreateAdminUser(ctx, "secops", pwHash, "operator")
	require.NoError(t, err)
	require.Equal(t, "secops", createdUser.Username)

	// 1. Failed login attempts
	for i := 1; i <= 3; i++ {
		_, _, err := svc.Login(ctx, "secops", "wrong-password", "192.168.1.10", "curl/8.0")
		require.ErrorIs(t, err, ErrInvalidCredentials)
	}

	// 4th attempt should be locked out
	_, _, err = svc.Login(ctx, "secops", pw, "192.168.1.10", "curl/8.0")
	require.ErrorIs(t, err, ErrAccountLocked)

	// Verify audit logs in PostgreSQL
	audits, err := st.ListAdminAudit(ctx, 10)
	require.NoError(t, err)
	require.NotEmpty(t, audits)

	var failedCount, lockedCount int
	for _, a := range audits {
		if a.Actor == "secops" && a.Action == "login_failed" {
			failedCount++
		}
		if a.Actor == "secops" && a.Action == "login_locked" {
			lockedCount++
		}
	}
	require.Equal(t, 3, failedCount)
	require.Equal(t, 2, lockedCount)

	// Reset lockout
	require.NoError(t, svc.ResetFailedLogin(ctx, "secops"))

	// Login successfully
	token, sess, err := svc.Login(ctx, "secops", pw, "192.168.1.10", "curl/8.0")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Equal(t, "secops", sess.Username)
	require.Equal(t, "operator", sess.Role)

	// Validate session in Redis
	validated, err := svc.ValidateSession(ctx, token)
	require.NoError(t, err)
	require.Equal(t, sess.ID, validated.ID)

	// Logout
	err = svc.Logout(ctx, token, "192.168.1.10")
	require.NoError(t, err)

	_, err = svc.ValidateSession(ctx, token)
	require.ErrorIs(t, err, ErrSessionNotFound)
}
