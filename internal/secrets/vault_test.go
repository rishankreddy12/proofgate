package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKubernetesAuthCachesToken(t *testing.T) {
	var logins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/auth/kubernetes/login", r.URL.Path)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		require.Equal(t, "proofgate", body["role"])
		require.Equal(t, "sa-jwt", body["jwt"])
		logins.Add(1)
		_, _ = w.Write([]byte(`{"auth":{"client_token":"hvs.abc","lease_duration":3600}}`))
	}))
	defer srv.Close()
	jwt := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(jwt, []byte("sa-jwt"), 0o600))
	ts := KubernetesAuth(srv.URL, "proofgate", jwt, srv.Client())
	for i := 0; i < 3; i++ {
		tok, err := ts(context.Background())
		require.NoError(t, err)
		require.Equal(t, "hvs.abc", tok)
	}
	require.EqualValues(t, 1, logins.Load())
}
