package adminauth

import (
	"context"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

func TestLoginFlowAndLockout(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore()
	mem := NewMemStore()
	cfg := config.AdminAuthConfig{
		MaxLoginAttempts:   3,
		LockoutDuration:    1 * time.Hour,
		SessionIdleTimeout: 30 * time.Minute,
		SessionAbsTimeout:  12 * time.Hour,
	}
	svc := NewServiceWithStore(fs, mem, cfg, nil)

	pw := "supersecret123"
	hash, err := HashPassword(pw)
	require.NoError(t, err)

	fs.users["admin"] = store.AdminUser{
		ID:           "u-admin",
		Username:     "admin",
		PasswordHash: hash,
		Role:         "admin",
		Enabled:      true,
	}

	// 1. Wrong password attempts
	for i := 1; i <= 3; i++ {
		_, _, err := svc.Login(ctx, "admin", "wrongpw", "127.0.0.1", "curl")
		require.ErrorIs(t, err, ErrInvalidCredentials)
	}

	// 4th attempt should hit lockout
	_, _, err = svc.Login(ctx, "admin", pw, "127.0.0.1", "curl")
	require.ErrorIs(t, err, ErrAccountLocked)

	// Reset lockout
	require.NoError(t, svc.ResetFailedLogin(ctx, "admin"))

	// Correct password succeeds
	token, sess, err := svc.Login(ctx, "admin", pw, "127.0.0.1", "curl")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Equal(t, "admin", sess.Username)

	// Logout invalidates session
	require.NoError(t, svc.Logout(ctx, token, "127.0.0.1"))
	_, err = svc.ValidateSession(ctx, token)
	require.ErrorIs(t, err, ErrSessionNotFound)
}

func TestLoginDisabledUser(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore()
	mem := NewMemStore()
	cfg := config.AdminAuthConfig{
		MaxLoginAttempts:   5,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 30 * time.Minute,
		SessionAbsTimeout:  12 * time.Hour,
	}
	svc := NewServiceWithStore(fs, mem, cfg, nil)

	hash, _ := HashPassword("pass123")
	fs.users["disabled_user"] = store.AdminUser{
		ID:           "u-dis",
		Username:     "disabled_user",
		PasswordHash: hash,
		Role:         "viewer",
		Enabled:      false,
	}

	_, _, err := svc.Login(ctx, "disabled_user", "pass123", "127.0.0.1", "agent")
	require.ErrorIs(t, err, ErrAccountDisabled)
}
