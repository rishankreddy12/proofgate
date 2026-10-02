package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/proofgate/proofgate/internal/adminauth"
	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/secrets"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/proofgate/proofgate/internal/telemetry"
	"github.com/proofgate/proofgate/internal/version"
	"github.com/redis/go-redis/v9"
	"gopkg.in/yaml.v3"
)
// ControlPlaneStore defines the persistence operations needed by the control plane handlers.
type ControlPlaneStore interface {
	adminauth.AdminStore
	ListAdminUsers(ctx context.Context) ([]store.AdminUser, error)
	CreateAdminUser(ctx context.Context, username string, passwordHash []byte, role string) (store.AdminUser, error)
	SetAdminUserEnabled(ctx context.Context, id string, enabled bool) error
	DeleteAdminUser(ctx context.Context, id string) error
	UpdateAdminUserPassword(ctx context.Context, id string, passwordHash []byte, mustChange bool) error
	ActiveCredential(ctx context.Context, provider string) (secrets.Sealed, int, error)
	PutCredential(ctx context.Context, provider string, sealed secrets.Sealed, actor string) (int, error)
	ListCredentials(ctx context.Context) ([]store.CredentialInfo, error)
	ListAdminAuditFiltered(ctx context.Context, filter store.AdminAuditFilter) ([]store.AdminAuditEvent, error)
}

type ControlPlaneDeps struct {
	State          *State
	Store          ControlPlaneStore
	Redis          *redis.Client
	AuthService    *adminauth.Service
	ReloadFunc     func() error
	PurgeCache     http.HandlerFunc
	PurgeSecrets   func()
	PublishControl func(ctx context.Context, op string, args map[string]string) error
	KEK            secrets.KEK
	KeyCache       *secrets.KeyCache
	HealthAdmin    http.HandlerFunc
	ChatHandler    http.HandlerFunc
	Handlers       *Handlers
	StartTime      time.Time
	AdminAddr      string
	ServerAddr     string
	Config         *config.Config
	TrustedProxies []*net.IPNet
	Metrics        *telemetry.Metrics
	EnablePprof    bool
}

func (deps *ControlPlaneDeps) clientIP(r *http.Request) string {
	var trusted []*net.IPNet
	if len(deps.TrustedProxies) > 0 {
		trusted = deps.TrustedProxies
	} else if deps.Config != nil {
		trusted, _ = deps.Config.Server.ParsedTrustedProxies()
	} else if deps.State != nil {
		if st := deps.State.Load(); st != nil && st.Config != nil {
			trusted, _ = st.Config.Server.ParsedTrustedProxies()
		}
	}
	return ClientIP(r, trusted)
}

func isPgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}
	return false
}

func (deps *ControlPlaneDeps) handleStatus(w http.ResponseWriter, r *http.Request) {
	st := deps.State.Load()
	providersCount := 0
	routesCount := 0
	if st != nil && st.Config != nil {
		providersCount = len(st.Config.Providers)
		routesCount = len(st.Config.Routes)
	}

	uptime := int64(time.Since(deps.StartTime).Seconds())
	writeJSON(w, http.StatusOK, map[string]any{
		"version":            version.Version,
		"uptime_seconds":     uptime,
		"server_addr":        deps.ServerAddr,
		"admin_addr":         deps.AdminAddr,
		"providers":          providersCount,
		"routes":             routesCount,
		"admin_auth_enabled": deps.AuthService != nil,
	})
}

func (deps *ControlPlaneDeps) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	st := deps.State.Load()
	if st == nil || st.Config == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":   "internal_error",
			"message": "server configuration not loaded",
		})
		return
	}

	redacted := config.Redact(st.Config)
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, redacted)
		return
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	_ = yaml.NewEncoder(w).Encode(redacted)
}

func (deps *ControlPlaneDeps) handleReloadConfig(w http.ResponseWriter, r *http.Request) {
	sess := adminauth.GetAdminPrincipal(r.Context())
	ip := deps.clientIP(r)

	if err := deps.ReloadFunc(); err != nil {
		if deps.Store != nil && sess != nil {
			if aerr := deps.Store.RecordAdminAudit(r.Context(), sess.Username, "config.reload", "", map[string]any{"error": err.Error()}, ip, "error"); aerr != nil && deps.Metrics != nil {
				deps.Metrics.AdminAuditFailures.Inc()
			}
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":   "reload_failed",
			"message": err.Error(),
		})
		return
	}

	if deps.Store != nil && sess != nil {
		if aerr := deps.Store.RecordAdminAudit(r.Context(), sess.Username, "config.reload", "", nil, ip, "ok"); aerr != nil && deps.Metrics != nil {
			deps.Metrics.AdminAuditFailures.Inc()
		}
	}
	if deps.PublishControl != nil {
		_ = deps.PublishControl(r.Context(), "reload", nil)
	}
	w.WriteHeader(http.StatusNoContent)
}

type ProviderSummary struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	BaseURL   string `json:"base_url"`
	KeySource string `json:"key_source"`
	HasKey    bool   `json:"has_key"`
}

func (deps *ControlPlaneDeps) handleListProviders(w http.ResponseWriter, r *http.Request) {
	st := deps.State.Load()
	if st == nil || st.Config == nil {
		writeJSON(w, http.StatusOK, []ProviderSummary{})
		return
	}

	out := make([]ProviderSummary, 0, len(st.Config.Providers))
	for _, p := range st.Config.Providers {
		keySource := "none"
		hasKey := false
		if p.APIKeyEnv != "" {
			keySource = "env:" + p.APIKeyEnv
			hasKey = os.Getenv(p.APIKeyEnv) != ""
		} else if p.APIKeyDB {
			keySource = "db"
			if deps.Store != nil {
				if _, _, err := deps.Store.ActiveCredential(r.Context(), p.Name); err == nil {
					hasKey = true
				}
			}
		}

		out = append(out, ProviderSummary{
			Name:      p.Name,
			Type:      p.Type,
			BaseURL:   p.BaseURL,
			KeySource: keySource,
			HasKey:    hasKey,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (deps *ControlPlaneDeps) handleGetProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	st := deps.State.Load()
	if st == nil || st.Config == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "provider not found"})
		return
	}

	for _, p := range st.Config.Providers {
		if p.Name == name {
			keySource := "none"
			hasKey := false
			if p.APIKeyEnv != "" {
				keySource = "env:" + p.APIKeyEnv
				hasKey = os.Getenv(p.APIKeyEnv) != ""
			} else if p.APIKeyDB {
				keySource = "db"
				if deps.Store != nil {
					if _, _, err := deps.Store.ActiveCredential(r.Context(), p.Name); err == nil {
						hasKey = true
					}
				}
			}

			writeJSON(w, http.StatusOK, ProviderSummary{
				Name:      p.Name,
				Type:      p.Type,
				BaseURL:   p.BaseURL,
				KeySource: keySource,
				HasKey:    hasKey,
			})
			return
		}
	}

	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "provider not found"})
}

func (deps *ControlPlaneDeps) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	st := deps.State.Load()
	if st == nil || st.Registry == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "provider not found"})
		return
	}

	p, ok := st.Registry.Get(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "provider not registered"})
		return
	}

	// Find model from router targets if available
	model := ""
	if st.Router != nil {
		for _, route := range st.Router.Routes() {
			for _, t := range route.Targets {
				if t.Provider == name {
					model = t.Model
					break
				}
			}
			if model != "" {
				break
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	one := 1
	start := time.Now()
	stream, err := p.ChatStream(ctx, model, &api.ChatRequest{
		MaxTokens: &one,
		Messages:  []api.Message{{Role: "user", Content: api.Content{Text: "ping"}}},
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"provider": name,
			"status":   "error",
			"error":    err.Error(),
		})
		return
	}
	defer stream.Close()

	if _, err := stream.Recv(); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"provider": name,
			"status":   "error",
			"error":    err.Error(),
		})
		return
	}

	latency := time.Since(start).Milliseconds()
	writeJSON(w, http.StatusOK, map[string]any{
		"provider":   name,
		"status":     "ok",
		"latency_ms": latency,
	})
}

type SetCredentialRequest struct {
	APIKey string `json:"api_key"`
}

func (deps *ControlPlaneDeps) handleSetCredential(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sess := adminauth.GetAdminPrincipal(r.Context())
	ip := deps.clientIP(r)

	var req SetCredentialRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if req.APIKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "api_key is required"})
		return
	}

	if deps.KEK == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": "KEK not configured on server"})
		return
	}

	sealed, err := secrets.Seal(r.Context(), deps.KEK, []byte(req.APIKey), secrets.AAD(name))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": "failed to seal credential: " + err.Error()})
		return
	}

	actor := "admin"
	if sess != nil {
		actor = sess.Username
	}

	version, err := deps.Store.PutCredential(r.Context(), name, sealed, actor)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": "failed to store credential: " + err.Error()})
		return
	}

	if deps.KeyCache != nil {
		deps.KeyCache.Purge()
	}

	_ = deps.Store.RecordAdminAudit(r.Context(), actor, "provider.credential.set", name, map[string]any{"version": version}, ip, "ok")
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": name,
		"version":  version,
	})
}

func (deps *ControlPlaneDeps) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	if deps.Store == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	creds, err := deps.Store.ListCredentials(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, creds)
}

func (deps *ControlPlaneDeps) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sess := adminauth.GetAdminPrincipal(r.Context())
	userFilter := r.URL.Query().Get("user")
	if sess != nil && sess.Role != "admin" {
		userFilter = sess.Username
	}

	sessions, err := deps.AuthService.ListSessions(r.Context(), userFilter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (deps *ControlPlaneDeps) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := adminauth.GetAdminPrincipal(r.Context())
	ip := deps.clientIP(r)

	if err := deps.AuthService.RevokeSession(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": err.Error()})
		return
	}

	actor := "admin"
	if sess != nil {
		actor = sess.Username
	}
	if deps.Store != nil {
		_ = deps.Store.RecordAdminAudit(r.Context(), actor, "session.revoke", id, map[string]any{"session_id": id}, ip, "ok")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (deps *ControlPlaneDeps) handleRevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	sess := adminauth.GetAdminPrincipal(r.Context())
	ip := deps.clientIP(r)

	userFilter := r.URL.Query().Get("user")
	if sess != nil && sess.Role != "admin" {
		userFilter = sess.Username
	}

	count, err := deps.AuthService.RevokeAllUserSessions(r.Context(), userFilter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": err.Error()})
		return
	}

	actor := "admin"
	if sess != nil {
		actor = sess.Username
	}
	if deps.Store != nil {
		_ = deps.Store.RecordAdminAudit(r.Context(), actor, "session.revoke_all", userFilter, map[string]any{"count": count}, ip, "ok")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"revoked_count": count,
	})
}

type CreateUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (deps *ControlPlaneDeps) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	sess := adminauth.GetAdminPrincipal(r.Context())
	ip := deps.clientIP(r)

	var req CreateUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "username and password are required"})
		return
	}

	if len(req.Username) > 64 || !adminauth.ValidateUsername(req.Username) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "username must be 1-64 characters matching [A-Za-z0-9._@-]"})
		return
	}

	if err := adminauth.ValidatePassword(req.Password, req.Username); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": err.Error()})
		return
	}

	if req.Role == "" {
		req.Role = "admin"
	}
	if req.Role != "admin" && req.Role != "operator" && req.Role != "viewer" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "role must be 'admin', 'operator', or 'viewer'"})
		return
	}

	pwHash, err := adminauth.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": "failed to hash password"})
		return
	}

	u, err := deps.Store.CreateAdminUser(r.Context(), req.Username, pwHash, req.Role)
	if err != nil {
		if isPgUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "conflict", "message": "user already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": "failed to create user"})
		return
	}

	actor := "admin"
	if sess != nil {
		actor = sess.Username
	}
	_ = deps.Store.RecordAdminAudit(r.Context(), actor, "user.create", u.Username, map[string]any{"role": u.Role}, ip, "ok")
	writeJSON(w, http.StatusCreated, u)
}

func (deps *ControlPlaneDeps) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := deps.Store.ListAdminUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (deps *ControlPlaneDeps) handleGetUser(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	u, err := deps.Store.GetAdminUser(r.Context(), username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "user not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type SetUserEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

func (deps *ControlPlaneDeps) handleSetUserEnabled(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	sess := adminauth.GetAdminPrincipal(r.Context())
	ip := deps.clientIP(r)

	var req SetUserEnabledRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	u, err := deps.Store.GetAdminUser(r.Context(), username)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "user not found"})
		return
	}

	if err := deps.Store.SetAdminUserEnabled(r.Context(), u.ID, req.Enabled); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "conflict", "message": "cannot disable the last enabled admin"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": "failed to update user status"})
		return
	}

	if !req.Enabled && deps.AuthService != nil {
		_, _ = deps.AuthService.RevokeAllUserSessions(r.Context(), username)
	}

	actor := "admin"
	if sess != nil {
		actor = sess.Username
	}
	action := "user.enable"
	if !req.Enabled {
		action = "user.disable"
	}
	_ = deps.Store.RecordAdminAudit(r.Context(), actor, action, username, nil, ip, "ok")
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "enabled": req.Enabled})
}

func (deps *ControlPlaneDeps) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	sess := adminauth.GetAdminPrincipal(r.Context())
	ip := deps.clientIP(r)

	u, err := deps.Store.GetAdminUser(r.Context(), username)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "user not found"})
		return
	}

	if err := deps.Store.DeleteAdminUser(r.Context(), u.ID); err != nil {
		if errors.Is(err, store.ErrLastAdmin) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "conflict", "message": "cannot delete the last enabled admin"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": "failed to delete user"})
		return
	}

	// Revoke sessions after successful deletion
	if deps.AuthService != nil {
		_, _ = deps.AuthService.RevokeAllUserSessions(r.Context(), username)
	}

	actor := "admin"
	if sess != nil {
		actor = sess.Username
	}
	_ = deps.Store.RecordAdminAudit(r.Context(), actor, "user.delete", username, nil, ip, "ok")
	w.WriteHeader(http.StatusNoContent)
}

type ChangePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (deps *ControlPlaneDeps) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	username := r.PathValue("username")
	sess := adminauth.GetAdminPrincipal(r.Context())
	ip := deps.clientIP(r)

	if sess == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized", "message": "not authenticated"})
		return
	}

	isSelf := sess.Username == username
	isAdmin := adminauth.HasPermission(sess.Role, adminauth.PermUserManage)

	if !isSelf && !isAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden", "message": "cannot change another user's password"})
		return
	}

	var req ChangePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "new_password is required"})
		return
	}

	u, err := deps.Store.GetAdminUser(r.Context(), username)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "user not found"})
		return
	}

	// Always require old password for self-service, even for admins
	if isSelf {
		if req.OldPassword == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "old_password is required"})
			return
		}
		if err := adminauth.CheckPassword(u.PasswordHash, req.OldPassword); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": "incorrect old password"})
			return
		}
	}

	if err := adminauth.ValidatePassword(req.NewPassword, username); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": err.Error()})
		return
	}

	newHash, err := adminauth.HashPassword(req.NewPassword)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": "failed to hash password"})
		return
	}

	mustChange := !isSelf && isAdmin
	if err := deps.Store.UpdateAdminUserPassword(r.Context(), u.ID, newHash, mustChange); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": "failed to update password"})
		return
	}

	// Revoke active sessions for target user
	if deps.AuthService != nil {
		_, _ = deps.AuthService.RevokeAllUserSessions(r.Context(), username)
	}

	_ = deps.Store.RecordAdminAudit(r.Context(), sess.Username, "user.change_password", username, map[string]any{"self": isSelf, "must_change": mustChange}, ip, "ok")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (deps *ControlPlaneDeps) handleCachePurge(w http.ResponseWriter, r *http.Request) {
	sess := adminauth.GetAdminPrincipal(r.Context())
	ip := deps.clientIP(r)

	deps.PurgeCache(w, r)

	if deps.Store != nil && sess != nil {
		if aerr := deps.Store.RecordAdminAudit(r.Context(), sess.Username, "cache.purge", "", nil, ip, "ok"); aerr != nil && deps.Metrics != nil {
			deps.Metrics.AdminAuditFailures.Inc()
		}
	}
	if deps.PublishControl != nil {
		_ = deps.PublishControl(r.Context(), "purge_cache", nil)
	}
}

func (deps *ControlPlaneDeps) handleListAudit(w http.ResponseWriter, r *http.Request) {
	if deps.Store == nil {
		writeJSON(w, http.StatusOK, []store.AdminAuditEvent{})
		return
	}

	q := r.URL.Query()
	var filter store.AdminAuditFilter

	if sinceStr := q.Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			filter.Since = t
		}
	}
	filter.Actor = q.Get("actor")
	filter.Action = q.Get("action")
	if limitStr := q.Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			filter.Limit = l
		}
	}

	events, err := deps.Store.ListAdminAuditFiltered(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "audit_error", "message": err.Error()})
		return
	}
	if events == nil {
		events = []store.AdminAuditEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

// RegisterAdminRoutes binds all administrative and control-plane routes to the provided ServeMux.
func RegisterAdminRoutes(admin *http.ServeMux, deps *ControlPlaneDeps, authEnabled bool) {
	if !authEnabled {
		// Backward-compatible unauthenticated registration
		if deps.HealthAdmin != nil {
			admin.HandleFunc("GET /admin/health", deps.HealthAdmin)
		}
		if deps.PurgeCache != nil {
			admin.HandleFunc("POST /admin/cache/purge", func(w http.ResponseWriter, r *http.Request) {
				deps.PurgeCache(w, r)
				if deps.PublishControl != nil {
					_ = deps.PublishControl(r.Context(), "purge_cache", nil)
				}
			})
		}
		admin.HandleFunc("POST /admin/secrets/purge", func(w http.ResponseWriter, r *http.Request) {
			if deps.PurgeSecrets != nil {
				deps.PurgeSecrets()
			}
			if deps.PublishControl != nil {
				_ = deps.PublishControl(r.Context(), "purge_secrets", nil)
			}
			w.WriteHeader(http.StatusOK)
		})
		admin.HandleFunc("POST /admin/reload", func(w http.ResponseWriter, r *http.Request) {
			if deps.ReloadFunc != nil {
				if err := deps.ReloadFunc(); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
			}
			if deps.PublishControl != nil {
				_ = deps.PublishControl(r.Context(), "reload", nil)
			}
			w.WriteHeader(http.StatusNoContent)
		})
		return
	}

	// 1. Unauthenticated authentication endpoints
	admin.HandleFunc("POST /admin/auth/login", deps.handleLogin)

	// 2. Unauthenticated monitoring endpoints (as required by Section 16 & 27)
	if deps.HealthAdmin != nil {
		admin.HandleFunc("GET /admin/health", deps.HealthAdmin)
	}

	// Middleware wrappers
	wrapAuth := func(h http.HandlerFunc) http.Handler {
		return deps.AuthService.RequireAuth(h)
	}
	wrapPerm := func(perm adminauth.Permission, h http.HandlerFunc) http.Handler {
		return deps.AuthService.RequireAuth(adminauth.RequirePermission(perm, h))
	}

	// 3. Authenticated session endpoints
	admin.Handle("POST /admin/auth/logout", wrapAuth(deps.handleLogout))
	admin.Handle("GET /admin/auth/whoami", wrapAuth(deps.handleWhoami))

	// 4. Authenticated Control-Plane endpoints (/admin/cp/*)
	admin.Handle("GET /admin/cp/status", wrapPerm(adminauth.PermStatusView, deps.handleStatus))
	admin.Handle("GET /admin/cp/health", wrapPerm(adminauth.PermHealthView, deps.HealthAdmin))
	admin.Handle("GET /admin/cp/config", wrapPerm(adminauth.PermConfigView, deps.handleGetConfig))
	admin.Handle("POST /admin/cp/config/reload", wrapPerm(adminauth.PermConfigReload, deps.handleReloadConfig))
	admin.Handle("POST /admin/cp/cache/purge", wrapPerm(adminauth.PermCachePurge, deps.handleCachePurge))
	admin.Handle("GET /admin/cp/audit", wrapPerm(adminauth.PermAuditView, deps.handleListAudit))

	// Providers
	admin.Handle("GET /admin/cp/providers", wrapPerm(adminauth.PermConfigView, deps.handleListProviders))
	admin.Handle("GET /admin/cp/providers/{name}", wrapPerm(adminauth.PermConfigView, deps.handleGetProvider))
	admin.Handle("POST /admin/cp/providers/{name}/test", wrapPerm(adminauth.PermProviderManage, deps.handleTestProvider))
	admin.Handle("POST /admin/cp/providers/{name}/credential", wrapPerm(adminauth.PermProviderManage, deps.handleSetCredential))
	admin.Handle("GET /admin/cp/providers/credentials", wrapPerm(adminauth.PermProviderManage, deps.handleListCredentials))

	// Sessions
	admin.Handle("GET /admin/cp/sessions", wrapPerm(adminauth.PermSessionManage, deps.handleListSessions))
	admin.Handle("DELETE /admin/cp/sessions/{id}", wrapPerm(adminauth.PermSessionManage, deps.handleRevokeSession))
	admin.Handle("DELETE /admin/cp/sessions", wrapPerm(adminauth.PermSessionManage, deps.handleRevokeAllSessions))

	// Users
	admin.Handle("POST /admin/cp/users", wrapPerm(adminauth.PermUserManage, deps.handleCreateUser))
	admin.Handle("GET /admin/cp/users", wrapPerm(adminauth.PermUserManage, deps.handleListUsers))
	admin.Handle("GET /admin/cp/users/{username}", wrapPerm(adminauth.PermUserManage, deps.handleGetUser))
	admin.Handle("PUT /admin/cp/users/{username}/enabled", wrapPerm(adminauth.PermUserManage, deps.handleSetUserEnabled))
	admin.Handle("DELETE /admin/cp/users/{username}", wrapPerm(adminauth.PermUserManage, deps.handleDeleteUser))
	admin.Handle("POST /admin/cp/users/{username}/password", wrapAuth(deps.handleChangePassword))

	// 5. Existing operational endpoints authenticated when admin_auth is enabled
	admin.Handle("POST /admin/cache/purge", wrapPerm(adminauth.PermCachePurge, deps.handleCachePurge))
	admin.Handle("POST /admin/secrets/purge", wrapPerm(adminauth.PermProviderManage, func(w http.ResponseWriter, r *http.Request) {
		if deps.PurgeSecrets != nil {
			deps.PurgeSecrets()
		}
		if deps.PublishControl != nil {
			_ = deps.PublishControl(r.Context(), "purge_secrets", nil)
		}
		w.WriteHeader(http.StatusOK)
	}))
	admin.Handle("POST /admin/reload", wrapPerm(adminauth.PermConfigReload, deps.handleReloadConfig))

	// 6. Interactive Chat endpoint
	admin.Handle("POST /admin/cp/chat", wrapPerm(adminauth.PermChatTest, deps.handleChat))

	// 7. Go runtime profiling (pprof) guarded by PermDebug, admin-only
	if deps.EnablePprof {
		admin.Handle("GET /debug/pprof/", wrapPerm(adminauth.PermDebug, pprof.Index))
		admin.Handle("GET /debug/pprof/cmdline", wrapPerm(adminauth.PermDebug, pprof.Cmdline))
		admin.Handle("GET /debug/pprof/profile", wrapPerm(adminauth.PermDebug, pprof.Profile))
		admin.Handle("GET /debug/pprof/symbol", wrapPerm(adminauth.PermDebug, pprof.Symbol))
		admin.Handle("GET /debug/pprof/trace", wrapPerm(adminauth.PermDebug, pprof.Trace))
	}
}

func (deps *ControlPlaneDeps) handleChat(w http.ResponseWriter, r *http.Request) {
	if deps.Handlers == nil && deps.ChatHandler == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":   "service_unavailable",
			"message": "chat handler not initialized",
		})
		return
	}
	sess := adminauth.GetAdminPrincipal(r.Context())
	actor := "admin"
	if sess != nil && sess.Username != "" {
		actor = sess.Username
	}

	var chatCfg config.AdminChatConfig
	if deps.Config != nil {
		chatCfg = deps.Config.AdminAuth.Chat
	} else if deps.State != nil {
		if st := deps.State.Load(); st != nil && st.Config != nil {
			chatCfg = st.Config.AdminAuth.Chat
		}
	}
	rpm := chatCfg.RPM
	if rpm == 0 {
		rpm = 60
	}
	tpm := chatCfg.TPM
	if tpm == 0 {
		tpm = 100_000
	}

	p := auth.Principal{
		TenantID:      "control-plane",
		TenantName:    "admin",
		KeyName:       "cp:" + actor,
		AllowDirect:   false,
		AllowedRoutes: chatCfg.AllowedRoutes,
		Tenant: store.TenantPolicy{
			RPM:              rpm,
			TPM:              tpm,
			MonthlyBudgetUSD: chatCfg.BudgetUSD,
		},
		Key: store.KeyPolicy{
			AllowDirect: false,
		},
	}
	r = r.WithContext(auth.WithPrincipal(r.Context(), p))
	if deps.ChatHandler != nil {
		deps.ChatHandler(w, r)
		return
	}
	if deps.Handlers != nil {
		deps.Handlers.Chat(w, r)
		return
	}
	writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "not_implemented", "message": "chat handler not configured"})
}
