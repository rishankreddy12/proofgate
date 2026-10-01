//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/adminauth"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

func adminURL() string {
	if v := os.Getenv("PROOFGATE_ADMIN_URL"); v != "" {
		return v
	}
	return "http://localhost:19090"
}

func TestAdminControlPlaneE2E(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, dsn())
	require.NoError(t, err)
	defer st.Close()

	adminBase := adminURL()

	// 1. Bootstrap: Create initial admin user
	username := fmt.Sprintf("admin-%d", time.Now().UnixNano())
	pw := "InitialPassword123!"
	hash, err := adminauth.HashPassword(pw)
	require.NoError(t, err)

	_, err = st.CreateAdminUser(ctx, username, hash, "admin")
	require.NoError(t, err)

	// 2. Login Failure: wrong password returns 401 (or 404 if admin_auth is disabled)
	badLogin, _ := json.Marshal(map[string]string{
		"username": username,
		"password": "wrongpassword",
	})
	resp, err := http.Post(adminBase+"/admin/auth/login", "application/json", bytes.NewReader(badLogin))
	if err == nil {
		defer resp.Body.Close()
		require.True(t, resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound)

		// If admin_auth is enabled, test the full control-plane lifecycle
		if resp.StatusCode == http.StatusUnauthorized {
			// 3. Successful Login
			goodLogin, _ := json.Marshal(map[string]string{
				"username": username,
				"password": pw,
			})
			loginResp, err := http.Post(adminBase+"/admin/auth/login", "application/json", bytes.NewReader(goodLogin))
			require.NoError(t, err)
			defer loginResp.Body.Close()
			require.Equal(t, http.StatusOK, loginResp.StatusCode)

			var loginResult struct {
				Token string `json:"token"`
			}
			bodyBytes, _ := io.ReadAll(loginResp.Body)
			require.NoError(t, json.Unmarshal(bodyBytes, &loginResult))
			require.NotEmpty(t, loginResult.Token)

			// 4. Whoami
			client := &http.Client{Timeout: 5 * time.Second}
			req, _ := http.NewRequestWithContext(ctx, "GET", adminBase+"/admin/auth/whoami", nil)
			req.Header.Set("Authorization", "Bearer "+loginResult.Token)
			whoamiResp, err := client.Do(req)
			require.NoError(t, err)
			defer whoamiResp.Body.Close()
			require.Equal(t, http.StatusOK, whoamiResp.StatusCode)

			// 5. Status
			req, _ = http.NewRequestWithContext(ctx, "GET", adminBase+"/admin/cp/status", nil)
			req.Header.Set("Authorization", "Bearer "+loginResult.Token)
			statusResp, err := client.Do(req)
			require.NoError(t, err)
			defer statusResp.Body.Close()
			require.Equal(t, http.StatusOK, statusResp.StatusCode)

			// 6. Logout
			req, _ = http.NewRequestWithContext(ctx, "POST", adminBase+"/admin/auth/logout", nil)
			req.Header.Set("Authorization", "Bearer "+loginResult.Token)
			logoutResp, err := client.Do(req)
			require.NoError(t, err)
			defer logoutResp.Body.Close()
			require.Equal(t, http.StatusOK, logoutResp.StatusCode)

			// 7. Post-logout request should fail with 401
			req, _ = http.NewRequestWithContext(ctx, "GET", adminBase+"/admin/auth/whoami", nil)
			req.Header.Set("Authorization", "Bearer "+loginResult.Token)
			postLogoutResp, err := client.Do(req)
			require.NoError(t, err)
			defer postLogoutResp.Body.Close()
			require.Equal(t, http.StatusUnauthorized, postLogoutResp.StatusCode)
		}
	}

	// 8. Verify Existing API key data-plane authentication works as expected
	key := newKey(t, store.TenantPolicy{RPM: 100})
	gateways := gatewayURLs()
	resp = chat(t, gateways[0], key)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}
