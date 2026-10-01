//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminUsersAndAudit(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// Initial count should be 0
	count, err := s.AdminUserCount(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	// Create user
	pwHash := []byte("$2a$12$somehashforpassword123456789012345678901234567890")
	u, err := s.CreateAdminUser(ctx, "admin1", pwHash, "admin")
	require.NoError(t, err)
	require.NotEmpty(t, u.ID)
	require.Equal(t, "admin1", u.Username)
	require.Equal(t, "admin", u.Role)
	require.True(t, u.Enabled)

	// Count should now be 1
	count, err = s.AdminUserCount(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Get by username
	byUser, err := s.GetAdminUser(ctx, "admin1")
	require.NoError(t, err)
	require.Equal(t, u.ID, byUser.ID)
	require.Equal(t, pwHash, byUser.PasswordHash)

	// Get by ID
	byID, err := s.GetAdminUserByID(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, "admin1", byID.Username)

	// List users
	users, err := s.ListAdminUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, "admin1", users[0].Username)

	// Update password
	newHash := []byte("$2a$12$newhashforpassword123456789012345678901234567890")
	err = s.UpdateAdminUserPassword(ctx, u.ID, newHash)
	require.NoError(t, err)
	byUser, err = s.GetAdminUser(ctx, "admin1")
	require.NoError(t, err)
	require.Equal(t, newHash, byUser.PasswordHash)

	// Disable user
	err = s.SetAdminUserEnabled(ctx, u.ID, false)
	require.NoError(t, err)
	byUser, err = s.GetAdminUser(ctx, "admin1")
	require.NoError(t, err)
	require.False(t, byUser.Enabled)

	// Audit log
	err = s.RecordAdminAudit(ctx, "admin1", "user.disable", u.ID, map[string]string{"reason": "testing"}, "127.0.0.1", "ok")
	require.NoError(t, err)

	events, err := s.ListAdminAudit(ctx, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "admin1", events[0].Actor)
	require.Equal(t, "user.disable", events[0].Action)
	require.Equal(t, u.ID, events[0].Target)
	require.Equal(t, "ok", events[0].Result)

	// Delete user
	err = s.DeleteAdminUser(ctx, u.ID)
	require.NoError(t, err)
	_, err = s.GetAdminUser(ctx, "admin1")
	require.ErrorIs(t, err, ErrNotFound)

	count, err = s.AdminUserCount(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}
