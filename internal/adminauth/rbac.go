// Package adminauth provides enterprise-grade capabilities, configuration, and structural components for the adminauth subsystem.
package adminauth

// Permission defines the core enterprise configuration and state for Permission.
// It is responsible for managing the lifecycle, validation, and schema of the Permission entity.
type Permission string

const (
	// PermUserManage defines a specific variation or structural setting for PermUserManage.
	PermUserManage Permission = "user:manage"
	// PermProviderManage defines a specific variation or structural setting for PermProviderManage.
	PermProviderManage Permission = "provider:manage"
	// PermConfigReload defines a specific variation or structural setting for PermConfigReload.
	PermConfigReload Permission = "config:reload"
	// PermConfigView defines a specific variation or structural setting for PermConfigView.
	PermConfigView Permission = "config:view"
	// PermCachePurge defines a specific variation or structural setting for PermCachePurge.
	PermCachePurge Permission = "cache:purge"
	// PermSessionManage defines a specific variation or structural setting for PermSessionManage.
	PermSessionManage Permission = "session:manage"
	// PermHealthView defines a specific variation or structural setting for PermHealthView.
	PermHealthView Permission = "health:view"
	// PermStatusView defines a specific variation or structural setting for PermStatusView.
	PermStatusView Permission = "status:view"
	// PermChatTest defines a specific variation or structural setting for PermChatTest.
	PermChatTest Permission = "chat:test"
	// PermDebug defines a specific variation or structural setting for PermDebug.
	PermDebug Permission = "debug:pprof"
	// PermAuditView defines a specific variation or structural setting for PermAuditView.
	PermAuditView Permission = "audit:view"
)

var rolePermissions = map[string][]Permission{
	"admin":    {PermUserManage, PermProviderManage, PermConfigReload, PermConfigView, PermCachePurge, PermSessionManage, PermHealthView, PermStatusView, PermChatTest, PermDebug, PermAuditView},
	"operator": {PermProviderManage, PermConfigReload, PermConfigView, PermCachePurge, PermHealthView, PermStatusView, PermChatTest},
	"viewer":   {PermConfigView, PermHealthView, PermStatusView},
}

// HasPermission executes the primary logic for the HasPermission operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func HasPermission(role string, perm Permission) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	for _, p := range perms {
		if p == perm {
			return true
		}
	}
	return false
}
