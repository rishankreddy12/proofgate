//go:build integration

package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func newRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	ctx := context.Background()
	c, err := tcredis.Run(ctx, "redis:7-alpine")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	u, err := c.ConnectionString(ctx)
	require.NoError(t, err)
	opt, err := redis.ParseURL(u)
	require.NoError(t, err)
	rdb := redis.NewClient(opt)
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func TestRedisSessionStore(t *testing.T) {
	rdb := newRedisClient(t)
	rs := NewRedisStore(rdb)
	ctx := context.Background()

	// 1. Set & Get
	err := rs.Set(ctx, "test:key:1", []byte("hello"), 5*time.Second)
	require.NoError(t, err)

	val, err := rs.Get(ctx, "test:key:1")
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), val)

	// 2. Scan
	err = rs.Set(ctx, "test:key:2", []byte("world"), 5*time.Second)
	require.NoError(t, err)
	keys, err := rs.Scan(ctx, "test:key:*")
	require.NoError(t, err)
	require.Len(t, keys, 2)

	// 3. Del
	err = rs.Del(ctx, "test:key:1", "test:key:2")
	require.NoError(t, err)

	_, err = rs.Get(ctx, "test:key:1")
	require.ErrorIs(t, err, ErrSessionNotFound)

	// 4. IncrWithTTL & GetWithTTL
	count, err := rs.IncrWithTTL(ctx, "test:counter", 10*time.Second)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)

	count, err = rs.IncrWithTTL(ctx, "test:counter", 10*time.Second)
	require.NoError(t, err)
	require.EqualValues(t, 2, count)

	valInt, ttl, err := rs.GetWithTTL(ctx, "test:counter")
	require.NoError(t, err)
	require.EqualValues(t, 2, valInt)
	require.True(t, ttl > 0 && ttl <= 10*time.Second)
}

func TestRedisSessionLifecycle(t *testing.T) {
	rdb := newRedisClient(t)
	fs := newFakeStore()
	cfg := config.AdminAuthConfig{
		MaxLoginAttempts:   5,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 10 * time.Minute,
		SessionAbsTimeout:  1 * time.Hour,
	}
	svc := NewService(fs, rdb, cfg, nil)
	ctx := context.Background()

	u := store.AdminUser{
		ID:       "u-real-redis",
		Username: "bob",
		Role:     "operator",
		Enabled:  true,
	}

	token, sess, err := svc.CreateSession(ctx, u, "10.0.0.1", "integration-client")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Equal(t, "bob", sess.Username)
	require.Equal(t, "operator", sess.Role)

	// Validate valid session
	validated, err := svc.ValidateSession(ctx, token)
	require.NoError(t, err)
	require.Equal(t, sess.ID, validated.ID)
	require.Equal(t, "bob", validated.Username)

	// List sessions for bob
	sessions, err := svc.ListSessions(ctx, "bob")
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, sess.ID, sessions[0].ID)

	// Create second session for bob
	token2, sess2, err := svc.CreateSession(ctx, u, "10.0.0.2", "integration-client-2")
	require.NoError(t, err)
	require.NotEmpty(t, token2)
	require.Equal(t, "bob", sess2.Username)

	sessions, err = svc.ListSessions(ctx, "bob")
	require.NoError(t, err)
	require.Len(t, sessions, 2)

	// Revoke one session by ID
	err = svc.RevokeSession(ctx, sess.ID)
	require.NoError(t, err)

	_, err = svc.ValidateSession(ctx, token)
	require.ErrorIs(t, err, ErrSessionNotFound)

	// Session 2 is still valid
	_, err = svc.ValidateSession(ctx, token2)
	require.NoError(t, err)

	// Revoke all sessions for bob
	count, err := svc.RevokeAllUserSessions(ctx, "bob")
	require.NoError(t, err)
	require.Equal(t, 1, count)

	_, err = svc.ValidateSession(ctx, token2)
	require.ErrorIs(t, err, ErrSessionNotFound)
}

func TestRedisSessionTimeouts(t *testing.T) {
	rdb := newRedisClient(t)
	fs := newFakeStore()
	cfg := config.AdminAuthConfig{
		MaxLoginAttempts:   5,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 100 * time.Millisecond,
		SessionAbsTimeout:  400 * time.Millisecond,
	}
	svc := NewService(fs, rdb, cfg, nil)
	ctx := context.Background()

	u := store.AdminUser{
		ID:       "u-timeout",
		Username: "timeout_user",
		Role:     "viewer",
		Enabled:  true,
	}

	token, _, err := svc.CreateSession(ctx, u, "127.0.0.1", "agent")
	require.NoError(t, err)

	// Immediate validation succeeds
	_, err = svc.ValidateSession(ctx, token)
	require.NoError(t, err)

	// Wait past idle timeout
	time.Sleep(150 * time.Millisecond)
	_, err = svc.ValidateSession(ctx, token)
	require.True(t, errors.Is(err, ErrSessionExpired) || errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrSessionIdle))
}
