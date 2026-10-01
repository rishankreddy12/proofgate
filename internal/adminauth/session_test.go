package adminauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeStore struct {
	users map[string]store.AdminUser
	audit []store.AdminAuditEvent
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users: make(map[string]store.AdminUser),
	}
}

func (f *fakeStore) GetAdminUser(ctx context.Context, username string) (store.AdminUser, error) {
	u, ok := f.users[username]
	if !ok {
		return store.AdminUser{}, store.ErrNotFound
	}
	return u, nil
}

func (f *fakeStore) RecordAdminAudit(ctx context.Context, actor, action, target string, detail any, ip, result string) error {
	f.audit = append(f.audit, store.AdminAuditEvent{
		Actor:  actor,
		Action: action,
		Target: target,
		IP:     ip,
		Result: result,
	})
	return nil
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore()
	mem := NewMemStore()
	cfg := config.AdminAuthConfig{
		MaxLoginAttempts:   5,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 10 * time.Minute,
		SessionAbsTimeout:  1 * time.Hour,
	}
	svc := NewServiceWithStore(fs, mem, cfg, nil)

	u := store.AdminUser{
		ID:       "u-123",
		Username: "alice",
		Role:     "admin",
		Enabled:  true,
	}

	token, sess, err := svc.CreateSession(ctx, u, "127.0.0.1", "test-agent")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Equal(t, "alice", sess.Username)
	require.Equal(t, "admin", sess.Role)

	// Validate valid session
	validated, err := svc.ValidateSession(ctx, token)
	require.NoError(t, err)
	require.Equal(t, sess.ID, validated.ID)
	require.Equal(t, "alice", validated.Username)

	// List sessions
	sessions, err := svc.ListSessions(ctx, "alice")
	require.NoError(t, err)
	require.Len(t, sessions, 1)

	// Revoke by ID
	err = svc.RevokeSession(ctx, sess.ID)
	require.NoError(t, err)

	_, err = svc.ValidateSession(ctx, token)
	require.ErrorIs(t, err, ErrSessionNotFound)
}

func TestSessionTimeouts(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore()
	mem := NewMemStore()
	cfg := config.AdminAuthConfig{
		MaxLoginAttempts:   5,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 100 * time.Millisecond,
		SessionAbsTimeout:  500 * time.Millisecond,
	}
	svc := NewServiceWithStore(fs, mem, cfg, nil)

	u := store.AdminUser{
		ID:       "u-456",
		Username: "bob",
		Role:     "operator",
		Enabled:  true,
	}

	token, _, err := svc.CreateSession(ctx, u, "127.0.0.1", "test")
	require.NoError(t, err)

	// Wait past idle timeout
	time.Sleep(150 * time.Millisecond)
	_, err = svc.ValidateSession(ctx, token)
	require.ErrorIs(t, err, ErrSessionIdle)

	// New session for absolute timeout
	token2, _, err := svc.CreateSession(ctx, u, "127.0.0.1", "test")
	require.NoError(t, err)

	// Keep alive with active calls until absolute timeout
	time.Sleep(550 * time.Millisecond)
	_, err = svc.ValidateSession(ctx, token2)
	require.True(t, errors.Is(err, ErrSessionExpired) || errors.Is(err, ErrSessionNotFound))
}

func TestRevokeAllUserSessions(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore()
	mem := NewMemStore()
	cfg := config.AdminAuthConfig{
		MaxLoginAttempts:   5,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 10 * time.Minute,
		SessionAbsTimeout:  1 * time.Hour,
	}
	svc := NewServiceWithStore(fs, mem, cfg, nil)

	u1 := store.AdminUser{ID: "u-1", Username: "user1", Role: "viewer", Enabled: true}
	u2 := store.AdminUser{ID: "u-2", Username: "user2", Role: "viewer", Enabled: true}

	t1, _, err := svc.CreateSession(ctx, u1, "127.0.0.1", "agent")
	require.NoError(t, err)
	t2, _, err := svc.CreateSession(ctx, u1, "127.0.0.1", "agent")
	require.NoError(t, err)
	t3, _, err := svc.CreateSession(ctx, u2, "127.0.0.1", "agent")
	require.NoError(t, err)

	count, err := svc.RevokeAllUserSessions(ctx, "user1")
	require.NoError(t, err)
	require.Equal(t, 2, count)

	_, err = svc.ValidateSession(ctx, t1)
	require.Error(t, err)
	_, err = svc.ValidateSession(ctx, t2)
	require.Error(t, err)

	// user2's session must still be valid
	v3, err := svc.ValidateSession(ctx, t3)
	require.NoError(t, err)
	require.Equal(t, "user2", v3.Username)
}

func TestConcurrentValidateAndRevoke(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore()
	mem := NewMemStore()
	cfg := config.AdminAuthConfig{
		MaxLoginAttempts:   5,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 10 * time.Minute,
		SessionAbsTimeout:  1 * time.Hour,
	}
	svc := NewServiceWithStore(fs, mem, cfg, nil)
	u := store.AdminUser{ID: "u-conc", Username: "concur", Role: "admin", Enabled: true}

	for i := 0; i < 500; i++ {
		token, sess, err := svc.CreateSession(ctx, u, "127.0.0.1", "test")
		require.NoError(t, err)

		done := make(chan struct{})
		go func() {
			for j := 0; j < 5; j++ {
				_, _ = svc.ValidateSession(ctx, token)
			}
			close(done)
		}()

		_ = svc.RevokeSession(ctx, sess.ID)
		<-done

		// Crucial assertion: once revoked, session must NEVER be resurrected by ValidateSession touch
		_, err = svc.ValidateSession(ctx, token)
		require.ErrorIs(t, err, ErrSessionNotFound, "session %s resurrected after revocation", sess.ID)
	}
}

func TestListSessionsIndependentOfUnrelatedKeys(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore()
	mem := NewMemStore()
	cfg := config.AdminAuthConfig{
		MaxLoginAttempts:   5,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 10 * time.Minute,
		SessionAbsTimeout:  1 * time.Hour,
	}
	svc := NewServiceWithStore(fs, mem, cfg, nil)
	u := store.AdminUser{ID: "u-idx", Username: "indexed_user", Role: "operator", Enabled: true}

	// Seed 10,000 unrelated keys into store
	for i := 0; i < 10000; i++ {
		_ = mem.Set(ctx, "unrelated:key:"+string(rune(i)), []byte("noise"), time.Hour)
	}

	// Create user sessions
	t1, _, err := svc.CreateSession(ctx, u, "127.0.0.1", "test")
	require.NoError(t, err)
	t2, _, err := svc.CreateSession(ctx, u, "127.0.0.1", "test")
	require.NoError(t, err)

	sessions, err := svc.ListSessions(ctx, "indexed_user")
	require.NoError(t, err)
	require.Len(t, sessions, 2)

	require.NotEmpty(t, t1)
	require.NotEmpty(t, t2)
}
