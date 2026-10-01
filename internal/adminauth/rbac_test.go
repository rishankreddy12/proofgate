package adminauth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRBAC(t *testing.T) {
	// Admin has all permissions
	require.True(t, HasPermission("admin", PermUserManage))
	require.True(t, HasPermission("admin", PermProviderManage))
	require.True(t, HasPermission("admin", PermConfigReload))
	require.True(t, HasPermission("admin", PermConfigView))
	require.True(t, HasPermission("admin", PermCachePurge))
	require.True(t, HasPermission("admin", PermSessionManage))
	require.True(t, HasPermission("admin", PermHealthView))
	require.True(t, HasPermission("admin", PermStatusView))

	// Operator has operational permissions, but NOT user/session manage
	require.False(t, HasPermission("operator", PermUserManage))
	require.False(t, HasPermission("operator", PermSessionManage))
	require.True(t, HasPermission("operator", PermProviderManage))
	require.True(t, HasPermission("operator", PermConfigReload))
	require.True(t, HasPermission("operator", PermConfigView))
	require.True(t, HasPermission("operator", PermCachePurge))
	require.True(t, HasPermission("operator", PermHealthView))
	require.True(t, HasPermission("operator", PermStatusView))

	// Viewer is read-only
	require.False(t, HasPermission("viewer", PermUserManage))
	require.False(t, HasPermission("viewer", PermProviderManage))
	require.False(t, HasPermission("viewer", PermConfigReload))
	require.False(t, HasPermission("viewer", PermCachePurge))
	require.False(t, HasPermission("viewer", PermSessionManage))
	require.True(t, HasPermission("viewer", PermConfigView))
	require.True(t, HasPermission("viewer", PermHealthView))
	require.True(t, HasPermission("viewer", PermStatusView))

	// Unknown role has no permissions
	require.False(t, HasPermission("guest", PermStatusView))
	require.False(t, HasPermission("", PermStatusView))
}
