//go:build e2e

package e2e

import (
	"bytes"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

// The mock provider's key is stored sealed via proofgatectl in scripts/e2e_security_setup.sh.
const mockProviderKey = "sk-e2e-mock-provider-key-0123456789"

func TestProviderKeyNeverLeaves(t *testing.T) {
	gateways := gatewayURLs()
	key := newKey(t, store.TenantPolicy{})
	bodies := []string{
		`{"model":"default","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"nope","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"default","messages":[]}`,
	}
	for _, b := range bodies {
		req, _ := http.NewRequest("POST", gateways[0]+"/v1/chat/completions", bytes.NewBufferString(b))
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		out, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NotContains(t, string(out), mockProviderKey)
		for k, v := range resp.Header {
			require.NotContains(t, strings.Join(v, ","), mockProviderKey, k)
		}
	}
	logs, err := exec.Command("docker", "compose", "-f", "../deploy/docker-compose.yml", "logs", "gateway-1").CombinedOutput()
	require.NoError(t, err)
	require.NotContains(t, string(logs), mockProviderKey)
	require.NotContains(t, string(logs), key, "ProofGate keys must not be logged either")
}

func TestAdminPortNotPublic(t *testing.T) {
	out, err := exec.Command("docker", "compose", "-f", "../deploy/docker-compose.yml", "port", "gateway-1", "9090").CombinedOutput()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(strings.TrimSpace(string(out)), "127.0.0.1:"), "admin port must bind to localhost only, got %s", out)
}

func TestClientCannotChooseUpstreamURL(t *testing.T) {
	gateways := gatewayURLs()
	key := newKey(t, store.TenantPolicy{})
	req, _ := http.NewRequest("POST", gateways[0]+"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"http://169.254.169.254/latest","messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Contains(t, []int{400, 403, 404}, resp.StatusCode, "upstream URLs come only from config (no SSRF)")
}
