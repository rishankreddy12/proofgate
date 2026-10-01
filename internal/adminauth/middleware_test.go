package adminauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

func TestAuthMiddleware(t *testing.T) {
	ctx := context.Background()
	fs := newFakeStore()
	mem := NewMemStore()
	cfg := config.AdminAuthConfig{
		SessionIdleTimeout: 10 * time.Minute,
		SessionAbsTimeout:  1 * time.Hour,
	}
	svc := NewServiceWithStore(fs, mem, cfg, nil)

	u := store.AdminUser{ID: "u-1", Username: "alice", Role: "operator", Enabled: true}
	token, _, err := svc.CreateSession(ctx, u, "127.0.0.1", "test")
	require.NoError(t, err)

	handler := svc.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := GetAdminPrincipal(r.Context())
		if p == nil {
			http.Error(w, "no principal", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(p.Username + ":" + p.Role))
	}))

	// Case 1: Missing auth header
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Case 2: Invalid token
	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer pgadmin_invalidtoken")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Case 3: Valid token
	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "alice:operator", rec.Body.String())
}

func TestRequirePermission(t *testing.T) {
	ctx := context.Background()
	sess := &AdminSession{
		Username: "bob",
		Role:     "viewer",
	}
	ctx = WithAdminPrincipal(ctx, sess)

	protectedHandler := RequirePermission(PermUserManage, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Viewer cannot manage users -> 403 Forbidden
	req := httptest.NewRequest("POST", "/users", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	protectedHandler(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// Admin can manage users -> 200 OK
	adminCtx := WithAdminPrincipal(context.Background(), &AdminSession{Username: "admin", Role: "admin"})
	req = httptest.NewRequest("POST", "/users", nil).WithContext(adminCtx)
	rec = httptest.NewRecorder()
	protectedHandler(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}
