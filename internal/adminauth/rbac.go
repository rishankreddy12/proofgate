package adminauth

type Permission string

const (
	PermUserManage     Permission = "user:manage"
	PermProviderManage Permission = "provider:manage"
	PermConfigReload   Permission = "config:reload"
	PermConfigView     Permission = "config:view"
	PermCachePurge     Permission = "cache:purge"
	PermSessionManage  Permission = "session:manage"
	PermHealthView     Permission = "health:view"
	PermStatusView     Permission = "status:view"
	PermChatTest       Permission = "chat:test"
	PermDebug          Permission = "debug:pprof"
	PermAuditView      Permission = "audit:view"
)

var rolePermissions = map[string][]Permission{
	"admin":    {PermUserManage, PermProviderManage, PermConfigReload, PermConfigView, PermCachePurge, PermSessionManage, PermHealthView, PermStatusView, PermChatTest, PermDebug, PermAuditView},
	"operator": {PermProviderManage, PermConfigReload, PermConfigView, PermCachePurge, PermHealthView, PermStatusView, PermChatTest},
	"viewer":   {PermConfigView, PermHealthView, PermStatusView},
}

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
