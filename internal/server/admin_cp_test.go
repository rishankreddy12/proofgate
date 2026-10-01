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

	"github.com/proofgate/proofgate/internal/adminauth"
	"github.com/proofgate/proofgate/internal/config"
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
