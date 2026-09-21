//go:build integration

package secrets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	tcvault "github.com/testcontainers/testcontainers-go/modules/vault"
)

func TestVaultKEKRoundTrip(t *testing.T) {
	ctx := context.Background()
	c, err := tcvault.Run(ctx, "hashicorp/vault:1.17", tcvault.WithToken("root"),
		tcvault.WithInitCommand("secrets enable transit", "write -f transit/keys/proofgate"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	addr, err := c.HttpHostAddress(ctx)
	require.NoError(t, err)

	k := NewVaultKEK(addr, "proofgate", StaticToken("root"), nil)
	require.Equal(t, "vault:proofgate", k.ID())
	s, err := Seal(ctx, k, []byte("sk-ant-xyz"), AAD("anthropic"))
	require.NoError(t, err)
	require.Contains(t, string(s.WrappedDEK), "vault:v1:")
	pt, err := Open(ctx, k, s, AAD("anthropic"))
	require.NoError(t, err)
	require.Equal(t, "sk-ant-xyz", string(pt))
}
