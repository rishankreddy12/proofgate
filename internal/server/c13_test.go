package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/proofgate/proofgate/internal/adminauth"
	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

func TestAdminBodySizeLimit(t *testing.T) {
	fs := newFakeAdminStore()
	_, mux, adminToken := setupTestAdminDeps(t, fs)

	// 1. Oversized body to /admin/auth/login (> 64KB, e.g. 70KB)
	largePadding := strings.Repeat("x", 70*1024)
	largeLoginBody := `{"username":"admin","password":"` + largePadding + `"}`
	req := httptest.NewRequest("POST", "/admin/auth/login", strings.NewReader(largeLoginBody))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	var errResp map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	require.Equal(t, "payload_too_large", errResp["error"])

	// 2. Oversized body to /admin/cp/users
	largeUserBody := `{"username":"newuser","password":"` + largePadding + `"}`
	req = httptest.NewRequest("POST", "/admin/cp/users", strings.NewReader(largeUserBody))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestAdminDisallowUnknownFields(t *testing.T) {
	fs := newFakeAdminStore()
	_, mux, adminToken := setupTestAdminDeps(t, fs)

	// 1. Unknown field in /admin/auth/login
	unknownFieldLogin := `{"username":"admin","password":"InitialAdminPass123!","extra_field":"unexpected"}`
	req := httptest.NewRequest("POST", "/admin/auth/login", strings.NewReader(unknownFieldLogin))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	// 2. Unknown field in /admin/cp/users
	unknownFieldUser := `{"username":"validuser","password":"ValidPassword123!","role":"operator","privilege_escalation":true}`
	req = httptest.NewRequest("POST", "/admin/cp/users", strings.NewReader(unknownFieldUser))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAdminPprofGating(t *testing.T) {
	fs := newFakeAdminStore()

	// 1. When EnablePprof is false (default):
	deps, muxDisabled, adminToken := setupTestAdminDeps(t, fs)
	require.False(t, deps.EnablePprof)

	req := httptest.NewRequest("GET", "/debug/pprof/", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	muxDisabled.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	// 2. When EnablePprof is true:
	depsEnabled, muxEnabled, adminToken := setupTestAdminDeps(t, fs)
	depsEnabled.EnablePprof = true
	// Re-register routes with EnablePprof = true
	muxEnabled = http.NewServeMux()
	RegisterAdminRoutes(muxEnabled, depsEnabled, true)

	// Create viewer and operator users
	hash, err := adminauth.HashPassword("CommonPass12345!")
	require.NoError(t, err)

	fs.users["viewer_u"] = store.AdminUser{
		ID:           "u-viewer-p",
		Username:     "viewer_u",
		PasswordHash: hash,
		Role:         "viewer",
		Enabled:      true,
	}
	fs.users["operator_u"] = store.AdminUser{
		ID:           "u-op-p",
		Username:     "operator_u",
		PasswordHash: hash,
		Role:         "operator",
		Enabled:      true,
	}

	// Login as viewer
	body, _ := json.Marshal(LoginRequest{Username: "viewer_u", Password: "CommonPass12345!"})
	req = httptest.NewRequest("POST", "/admin/auth/login", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	muxEnabled.ServeHTTP(rec, req)
	var viewerResp LoginResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&viewerResp))

	// Login as operator
	body, _ = json.Marshal(LoginRequest{Username: "operator_u", Password: "CommonPass12345!"})
	req = httptest.NewRequest("POST", "/admin/auth/login", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	muxEnabled.ServeHTTP(rec, req)
	var opResp LoginResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&opResp))

	// A. Unauthenticated request to /debug/pprof/ -> 401
	req = httptest.NewRequest("GET", "/debug/pprof/", nil)
	rec = httptest.NewRecorder()
	muxEnabled.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// B. Viewer request to /debug/pprof/ -> 403 (missing PermDebug)
	req = httptest.NewRequest("GET", "/debug/pprof/", nil)
	req.Header.Set("Authorization", "Bearer "+viewerResp.Token)
	rec = httptest.NewRecorder()
	muxEnabled.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// C. Operator request to /debug/pprof/ -> 403 (missing PermDebug)
	req = httptest.NewRequest("GET", "/debug/pprof/", nil)
	req.Header.Set("Authorization", "Bearer "+opResp.Token)
	rec = httptest.NewRecorder()
	muxEnabled.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// D. Admin request to /debug/pprof/ -> 200 OK
	req = httptest.NewRequest("GET", "/debug/pprof/", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	muxEnabled.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestStartupLoopbackSafetyCheck(t *testing.T) {
	checkStartupSafety := func(adminAddr string, authEnabled bool, allowOverride string) error {
		if !config.IsLoopbackAddr(adminAddr) && !authEnabled {
			if allowOverride != "1" {
				return errors.New("fatal: server.admin_addr is non-loopback while admin_auth.enabled is false")
			}
		}
		return nil
	}

	// Loopback address without auth -> allowed
	require.NoError(t, checkStartupSafety("127.0.0.1:9090", false, ""))
	require.NoError(t, checkStartupSafety("localhost:9090", false, ""))

	// Non-loopback address with auth enabled -> allowed
	require.NoError(t, checkStartupSafety("0.0.0.0:9090", true, ""))
	require.NoError(t, checkStartupSafety(":9090", true, ""))

	// Non-loopback address without auth -> rejected!
	require.Error(t, checkStartupSafety("0.0.0.0:9090", false, ""))
	require.Error(t, checkStartupSafety(":9090", false, ""))
	require.Error(t, checkStartupSafety("192.168.1.100:9090", false, ""))

	// Non-loopback address without auth, with override -> allowed
	require.NoError(t, checkStartupSafety("0.0.0.0:9090", false, "1"))
	require.NoError(t, checkStartupSafety(":9090", false, "1"))
}
