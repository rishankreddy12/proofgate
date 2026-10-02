package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/proofgate/proofgate/internal/adminauth"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/secrets"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeAdminStore struct {
	users map[string]store.AdminUser
	audit []store.AdminAuditEvent
}

func newFakeAdminStore() *fakeAdminStore {
	return &fakeAdminStore{
		users: make(map[string]store.AdminUser),
	}
}

func (f *fakeAdminStore) GetAdminUser(ctx context.Context, username string) (store.AdminUser, error) {
	u, ok := f.users[username]
	if !ok {
		return store.AdminUser{}, store.ErrNotFound
	}
	return u, nil
}

func (f *fakeAdminStore) RecordAdminAudit(ctx context.Context, actor, action, target string, detail any, ip, result string) error {
	f.audit = append(f.audit, store.AdminAuditEvent{
		Actor:  actor,
		Action: action,
		Target: target,
		IP:     ip,
		Result: result,
	})
	return nil
}

func (f *fakeAdminStore) ListAdminUsers(ctx context.Context) ([]store.AdminUser, error) {
	out := make([]store.AdminUser, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, u)
	}
	return out, nil
}

func (f *fakeAdminStore) CreateAdminUser(ctx context.Context, username string, passwordHash []byte, role string) (store.AdminUser, error) {
	if _, ok := f.users[username]; ok {
		return store.AdminUser{}, &pgconn.PgError{Code: "23505"}
	}
	u := store.AdminUser{
		ID:           "u-" + username,
		Username:     username,
		PasswordHash: passwordHash,
		Role:         role,
		Enabled:      true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	f.users[username] = u
	return u, nil
}

func (f *fakeAdminStore) countActiveAdmins(excludeID string) int {
	c := 0
	for _, u := range f.users {
		if u.Role == "admin" && u.Enabled && u.ID != excludeID {
			c++
		}
	}
	return c
}

func (f *fakeAdminStore) SetAdminUserEnabled(ctx context.Context, id string, enabled bool) error {
	for name, u := range f.users {
		if u.ID == id {
			if !enabled && u.Role == "admin" && f.countActiveAdmins(id) == 0 {
				return store.ErrLastAdmin
			}
			u.Enabled = enabled
			f.users[name] = u
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeAdminStore) DeleteAdminUser(ctx context.Context, id string) error {
	for name, u := range f.users {
		if u.ID == id {
			if u.Role == "admin" && f.countActiveAdmins(id) == 0 {
				return store.ErrLastAdmin
			}
			delete(f.users, name)
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeAdminStore) UpdateAdminUserPassword(ctx context.Context, id string, passwordHash []byte, mustChange bool) error {
	for name, u := range f.users {
		if u.ID == id {
			u.PasswordHash = passwordHash
			u.MustChange = mustChange
			f.users[name] = u
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeAdminStore) ActiveCredential(ctx context.Context, provider string) (secrets.Sealed, int, error) {
	return secrets.Sealed{}, 0, store.ErrNotFound
}

func (f *fakeAdminStore) PutCredential(ctx context.Context, provider string, sealed secrets.Sealed, actor string) (int, error) {
	return 1, nil
}

func (f *fakeAdminStore) ListCredentials(ctx context.Context) ([]store.CredentialInfo, error) {
	return nil, nil
}

func (f *fakeAdminStore) ListAdminAuditFiltered(ctx context.Context, filter store.AdminAuditFilter) ([]store.AdminAuditEvent, error) {
	var out []store.AdminAuditEvent
	for _, a := range f.audit {
		if !filter.Since.IsZero() && a.TS.Before(filter.Since) {
			continue
		}
		if filter.Actor != "" && a.Actor != filter.Actor {
			continue
		}
		if filter.Action != "" && a.Action != filter.Action {
			continue
		}
		out = append(out, a)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

func TestAdminRoutesDisabledAuth(t *testing.T) {
	mux := http.NewServeMux()
	reloadCalled := false
	deps := &ControlPlaneDeps{
		ReloadFunc: func() error {
			reloadCalled = true
			return nil
		},
		HealthAdmin: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
		PurgeCache: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	}
	RegisterAdminRoutes(mux, deps, false)

	// Existing unauthenticated reload works
	req := httptest.NewRequest("POST", "/admin/reload", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.True(t, reloadCalled)

	// Control plane route is not registered
	req = httptest.NewRequest("GET", "/admin/cp/status", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminRoutesEnabledAuth(t *testing.T) {
	fs := newFakeAdminStore()
	mem := adminauth.NewMemStore()
	authCfg := config.AdminAuthConfig{
		MaxLoginAttempts:   5,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 30 * time.Minute,
		SessionAbsTimeout:  12 * time.Hour,
	}
	authSvc := adminauth.NewServiceWithStore(fs, mem, authCfg, nil)

	pw := "adminpass123"
	hash, err := adminauth.HashPassword(pw)
	require.NoError(t, err)

	fs.users["admin"] = store.AdminUser{
		ID:           "u-admin",
		Username:     "admin",
		PasswordHash: hash,
		Role:         "admin",
		Enabled:      true,
	}

	state := &State{}
	state.Store(&Runtime{
		Config: &config.Config{
			Providers: []config.ProviderConfig{
				{Name: "openai-test", Type: "openai", BaseURL: "https://api.openai.com"},
			},
			Routes: []config.RouteConfig{
				{Name: "default"},
			},
		},
	})

	mux := http.NewServeMux()
	deps := &ControlPlaneDeps{
		State:       state,
		AuthService: authSvc,
		ReloadFunc:  func() error { return nil },
		HealthAdmin: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
		PurgeCache: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
		StartTime:  time.Now().Add(-10 * time.Minute),
		AdminAddr:  "127.0.0.1:9090",
		ServerAddr: ":8080",
	}
	RegisterAdminRoutes(mux, deps, true)

	// 1. Unauthenticated call to /admin/cp/status should fail with 401
	req := httptest.NewRequest("GET", "/admin/cp/status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// 2. Login
	loginBody, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": pw,
	})
	req = httptest.NewRequest("POST", "/admin/auth/login", bytes.NewReader(loginBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var loginResp LoginResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&loginResp))
	require.NotEmpty(t, loginResp.Token)
	token := loginResp.Token

	// 3. Whoami
	req = httptest.NewRequest("GET", "/admin/auth/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var who WhoamiResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&who))
	require.Equal(t, "admin", who.Username)

	// 4. Status
	req = httptest.NewRequest("GET", "/admin/cp/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var statusResp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&statusResp))
	require.Equal(t, float64(1), statusResp["providers"])
	require.Equal(t, true, statusResp["admin_auth_enabled"])

	// 5. Providers list
	req = httptest.NewRequest("GET", "/admin/cp/providers", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var provs []ProviderSummary
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&provs))
	require.Len(t, provs, 1)
	require.Equal(t, "openai-test", provs[0].Name)

	// 6. Logout
	req = httptest.NewRequest("POST", "/admin/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// 7. Subsequent request fails
	req = httptest.NewRequest("GET", "/admin/cp/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAdminLoginLockoutRetryAfterFormat(t *testing.T) {
	fs := newFakeAdminStore()
	mem := adminauth.NewMemStore()
	authCfg := config.AdminAuthConfig{
		MaxLoginAttempts:   2,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 30 * time.Minute,
		SessionAbsTimeout:  12 * time.Hour,
	}
	authSvc := adminauth.NewServiceWithStore(fs, mem, authCfg, nil)

	hash, err := adminauth.HashPassword("correctpass")
	require.NoError(t, err)

	fs.users["admin"] = store.AdminUser{
		ID:           "u-admin",
		Username:     "admin",
		PasswordHash: hash,
		Role:         "admin",
		Enabled:      true,
	}

	mux := http.NewServeMux()
	deps := &ControlPlaneDeps{
		AuthService: authSvc,
		HealthAdmin: func(w http.ResponseWriter, r *http.Request) {},
		ReloadFunc:  func() error { return nil },
		PurgeCache:  func(w http.ResponseWriter, r *http.Request) {},
	}
	RegisterAdminRoutes(mux, deps, true)

	// Attempt 1: wrong password -> 401
	body, _ := json.Marshal(LoginRequest{Username: "admin", Password: "wrong"})
	req := httptest.NewRequest("POST", "/admin/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Attempt 2: wrong password -> 401 (hits threshold of 2)
	req = httptest.NewRequest("POST", "/admin/auth/login", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Attempt 3: account is now locked -> 429 Too Many Requests with integer Retry-After
	req = httptest.NewRequest("POST", "/admin/auth/login", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	retryAfter := rec.Header().Get("Retry-After")
	require.NotEmpty(t, retryAfter)
	// Must be an integer per RFC 9110 (e.g. "900", not "15m0s" or "900ns")
	secs, parseErr := strconv.Atoi(retryAfter)
	require.NoError(t, parseErr, "Retry-After must be a valid integer number of seconds, got %q", retryAfter)
	require.GreaterOrEqual(t, secs, 800)
	require.LessOrEqual(t, secs, 900)
}

func setupTestAdminDeps(t *testing.T, fs *fakeAdminStore) (*ControlPlaneDeps, *http.ServeMux, string) {
	mem := adminauth.NewMemStore()
	authCfg := config.AdminAuthConfig{
		MaxLoginAttempts:   5,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 30 * time.Minute,
		SessionAbsTimeout:  12 * time.Hour,
	}
	authSvc := adminauth.NewServiceWithStore(fs, mem, authCfg, nil)

	pw := "InitialAdminPass123!"
	hash, err := adminauth.HashPassword(pw)
	require.NoError(t, err)

	fs.users["admin"] = store.AdminUser{
		ID:           "u-admin",
		Username:     "admin",
		PasswordHash: hash,
		Role:         "admin",
		Enabled:      true,
	}

	mux := http.NewServeMux()
	deps := &ControlPlaneDeps{
		Store:       fs,
		AuthService: authSvc,
		HealthAdmin: func(w http.ResponseWriter, r *http.Request) {},
		ReloadFunc:  func() error { return nil },
		PurgeCache:  func(w http.ResponseWriter, r *http.Request) {},
		StartTime:   time.Now(),
		AdminAddr:   "127.0.0.1:9090",
		ServerAddr:  ":8080",
	}
	RegisterAdminRoutes(mux, deps, true)

	// Perform login to get admin token
	loginBody, _ := json.Marshal(LoginRequest{Username: "admin", Password: pw})
	req := httptest.NewRequest("POST", "/admin/auth/login", bytes.NewReader(loginBody))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp LoginResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	return deps, mux, resp.Token
}

func TestAdminUserValidationAndCreation(t *testing.T) {
	fs := newFakeAdminStore()
	_, mux, adminToken := setupTestAdminDeps(t, fs)

	// 1. Invalid username (contains invalid characters)
	body, _ := json.Marshal(CreateUserRequest{
		Username: "user!invalid",
		Password: "ValidPassword123!",
		Role:     "operator",
	})
	req := httptest.NewRequest("POST", "/admin/cp/users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 2. Short password (< 12 chars)
	body, _ = json.Marshal(CreateUserRequest{
		Username: "operator1",
		Password: "short",
		Role:     "operator",
	})
	req = httptest.NewRequest("POST", "/admin/cp/users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 3. Password equals username
	body, _ = json.Marshal(CreateUserRequest{
		Username: "testuser12345",
		Password: "testuser12345",
		Role:     "operator",
	})
	req = httptest.NewRequest("POST", "/admin/cp/users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 4. Valid user creation
	body, _ = json.Marshal(CreateUserRequest{
		Username: "valid_operator",
		Password: "StrongOperatorPassword123!",
		Role:     "operator",
	})
	req = httptest.NewRequest("POST", "/admin/cp/users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	// 5. Duplicate username -> 409 Conflict
	req = httptest.NewRequest("POST", "/admin/cp/users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestAdminLastAdminGuard(t *testing.T) {
	fs := newFakeAdminStore()
	_, mux, adminToken := setupTestAdminDeps(t, fs)

	// Disabling the only admin returns 409
	body, _ := json.Marshal(SetUserEnabledRequest{Enabled: false})
	req := httptest.NewRequest("PUT", "/admin/cp/users/admin/enabled", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)

	// Deleting the only admin returns 409
	req = httptest.NewRequest("DELETE", "/admin/cp/users/admin", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)

	// Create a second admin
	body, _ = json.Marshal(CreateUserRequest{
		Username: "admin2",
		Password: "SecondAdminPass123!",
		Role:     "admin",
	})
	req = httptest.NewRequest("POST", "/admin/cp/users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	// Login as admin2 to get a valid token
	admin2LoginBody, _ := json.Marshal(LoginRequest{Username: "admin2", Password: "SecondAdminPass123!"})
	req = httptest.NewRequest("POST", "/admin/auth/login", bytes.NewReader(admin2LoginBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var admin2Resp LoginResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&admin2Resp))
	admin2Token := admin2Resp.Token

	// Now disabling admin succeeds because admin2 is still active
	body, _ = json.Marshal(SetUserEnabledRequest{Enabled: false})
	req = httptest.NewRequest("PUT", "/admin/cp/users/admin/enabled", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Now admin2 is the sole remaining active admin. Disabling admin2 using admin2Token must return 409!
	req = httptest.NewRequest("PUT", "/admin/cp/users/admin2/enabled", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+admin2Token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestAdminPasswordChangeWorkflow(t *testing.T) {
	fs := newFakeAdminStore()
	_, mux, adminToken := setupTestAdminDeps(t, fs)

	// Create operator user
	opPw := "InitialOpPassword123!"
	body, _ := json.Marshal(CreateUserRequest{
		Username: "operator_test",
		Password: opPw,
		Role:     "operator",
	})
	req := httptest.NewRequest("POST", "/admin/cp/users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	// Login as operator
	loginBody, _ := json.Marshal(LoginRequest{Username: "operator_test", Password: opPw})
	req = httptest.NewRequest("POST", "/admin/auth/login", bytes.NewReader(loginBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var opLoginResp LoginResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&opLoginResp))
	opToken := opLoginResp.Token

	// 1. Self-change missing old password -> 400
	chgBody, _ := json.Marshal(ChangePasswordRequest{
		NewPassword: "NewOpPassword12345!",
	})
	req = httptest.NewRequest("POST", "/admin/cp/users/operator_test/password", bytes.NewReader(chgBody))
	req.Header.Set("Authorization", "Bearer "+opToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 2. Self-change wrong old password -> 400
	chgBody, _ = json.Marshal(ChangePasswordRequest{
		OldPassword: "wrongpassword123",
		NewPassword: "NewOpPassword12345!",
	})
	req = httptest.NewRequest("POST", "/admin/cp/users/operator_test/password", bytes.NewReader(chgBody))
	req.Header.Set("Authorization", "Bearer "+opToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 3. Self-change valid old and new password -> 200
	chgBody, _ = json.Marshal(ChangePasswordRequest{
		OldPassword: opPw,
		NewPassword: "NewOpPassword12345!",
	})
	req = httptest.NewRequest("POST", "/admin/cp/users/operator_test/password", bytes.NewReader(chgBody))
	req.Header.Set("Authorization", "Bearer "+opToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	u := fs.users["operator_test"]
	require.False(t, u.MustChange)

	// 4. Admin resets operator password -> must_change = true, revokes sessions
	adminResetBody, _ := json.Marshal(ChangePasswordRequest{
		NewPassword: "AdminResetPassword123!",
	})
	req = httptest.NewRequest("POST", "/admin/cp/users/operator_test/password", bytes.NewReader(adminResetBody))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	u = fs.users["operator_test"]
	require.True(t, u.MustChange)

	// Verify operator's session was revoked
	req = httptest.NewRequest("GET", "/admin/auth/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+opToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAdminChatRBAC(t *testing.T) {
	fs := newFakeAdminStore()
	deps, mux, adminToken := setupTestAdminDeps(t, fs)

	deps.ChatHandler = func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing principal", http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Principal-KeyName", p.KeyName)
		w.Header().Set("X-RPM", strconv.Itoa(p.Tenant.RPM))
		w.Header().Set("X-TPM", strconv.Itoa(p.Tenant.TPM))
		w.WriteHeader(http.StatusOK)
	}

	// Create viewer and operator users
	hash, err := adminauth.HashPassword("CommonPass12345!")
	require.NoError(t, err)

	fs.users["viewer_user"] = store.AdminUser{
		ID:           "u-viewer",
		Username:     "viewer_user",
		PasswordHash: hash,
		Role:         "viewer",
		Enabled:      true,
	}
	fs.users["operator_user"] = store.AdminUser{
		ID:           "u-op",
		Username:     "operator_user",
		PasswordHash: hash,
		Role:         "operator",
		Enabled:      true,
	}

	// Login as viewer
	body, _ := json.Marshal(LoginRequest{Username: "viewer_user", Password: "CommonPass12345!"})
	req := httptest.NewRequest("POST", "/admin/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var viewerResp LoginResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&viewerResp))

	// Viewer attempts POST /admin/cp/chat -> 403 Forbidden
	req = httptest.NewRequest("POST", "/admin/cp/chat", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+viewerResp.Token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// Login as operator
	body, _ = json.Marshal(LoginRequest{Username: "operator_user", Password: "CommonPass12345!"})
	req = httptest.NewRequest("POST", "/admin/auth/login", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var opResp LoginResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&opResp))

	// Operator attempts POST /admin/cp/chat -> 200 OK
	req = httptest.NewRequest("POST", "/admin/cp/chat", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+opResp.Token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "cp:operator_user", rec.Header().Get("X-Principal-KeyName"))
	require.Equal(t, "60", rec.Header().Get("X-RPM"))

	// Admin attempts POST /admin/cp/chat -> 200 OK
	req = httptest.NewRequest("POST", "/admin/cp/chat", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "cp:admin", rec.Header().Get("X-Principal-KeyName"))
}

func TestAdminAuditEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	fs := newFakeAdminStore()
	mem := adminauth.NewMemStore()
	authCfg := config.AdminAuthConfig{
		MaxLoginAttempts:   5,
		LockoutDuration:    15 * time.Minute,
		SessionIdleTimeout: 30 * time.Minute,
		SessionAbsTimeout:  12 * time.Hour,
	}
	authSvc := adminauth.NewServiceWithStore(fs, mem, authCfg, nil)

	deps := &ControlPlaneDeps{
		Store:       fs,
		AuthService: authSvc,
	}
	RegisterAdminRoutes(mux, deps, true)

	// Seed audit events
	ctx := context.Background()
	_ = fs.RecordAdminAudit(ctx, "alice", "config.reload", "", nil, "127.0.0.1", "ok")
	_ = fs.RecordAdminAudit(ctx, "bob", "cache.purge", "", nil, "127.0.0.1", "ok")
	_ = fs.RecordAdminAudit(ctx, "alice", "user.create", "charlie", nil, "127.0.0.1", "ok")

	// Create admin, operator, viewer users
	hash, _ := adminauth.HashPassword("CommonPass12345!")
	fs.users["admin_user"] = store.AdminUser{Username: "admin_user", PasswordHash: hash, Role: "admin", Enabled: true}
	fs.users["op_user"] = store.AdminUser{Username: "op_user", PasswordHash: hash, Role: "operator", Enabled: true}
	fs.users["view_user"] = store.AdminUser{Username: "view_user", PasswordHash: hash, Role: "viewer", Enabled: true}

	login := func(user string) string {
		body, _ := json.Marshal(LoginRequest{Username: user, Password: "CommonPass12345!"})
		req := httptest.NewRequest("POST", "/admin/auth/login", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var resp LoginResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		return resp.Token
	}

	adminTok := login("admin_user")
	opTok := login("op_user")
	viewTok := login("view_user")

	// 1. Viewer is forbidden (403)
	req := httptest.NewRequest("GET", "/admin/cp/audit", nil)
	req.Header.Set("Authorization", "Bearer "+viewTok)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 2. Operator is forbidden (403)
	req = httptest.NewRequest("GET", "/admin/cp/audit", nil)
	req.Header.Set("Authorization", "Bearer "+opTok)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// 3. Admin can list all audit events (200 OK)
	req = httptest.NewRequest("GET", "/admin/cp/audit", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var all []store.AdminAuditEvent
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&all))
	require.Len(t, all, 6) // 3 seeded + 3 logins

	// 4. Admin filter by actor
	req = httptest.NewRequest("GET", "/admin/cp/audit?actor=bob", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var bobEvents []store.AdminAuditEvent
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&bobEvents))
	require.Len(t, bobEvents, 1)
	require.Equal(t, "bob", bobEvents[0].Actor)
	require.Equal(t, "cache.purge", bobEvents[0].Action)
}
