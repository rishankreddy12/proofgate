package secrets

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestLocalKEK() KEK {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	k, err := NewLocalKEK(raw)
	if err != nil {
		panic(err)
	}
	return k
}

func TestMultiKEKIsolation(t *testing.T) {
	ctx := context.Background()

	defaultKEK := newTestLocalKEK()
	tenantAKEK := newTestLocalKEK()
	tenantBKEK := newTestLocalKEK()

	multi := NewMultiKEK(defaultKEK, map[string]KEK{
		"tenant-a": tenantAKEK,
		"tenant-b": tenantBKEK,
	})

	secretA := []byte("sk-tenant-a-secret-key-12345")
	secretB := []byte("sk-tenant-b-secret-key-67890")
	aadA := AAD("openai")
	aadB := AAD("anthropic")

	// 1. Seal for Tenant A
	sealedA, err := multi.SealForTenant(ctx, "tenant-a", secretA, aadA)
	require.NoError(t, err)
	require.Equal(t, tenantAKEK.ID(), sealedA.KEKID)

	// 2. Open via MultiKEK succeeds (resolves Tenant A KEK)
	openedA, err := Open(ctx, multi, sealedA, aadA)
	require.NoError(t, err)
	require.Equal(t, secretA, openedA)

	// 3. Opening Tenant A's credential with global default KEK fails
	_, err = Open(ctx, defaultKEK, sealedA, aadA)
	require.Error(t, err, "default KEK should not be able to decrypt tenant-specific credential")
	require.Contains(t, err.Error(), "credential was sealed with a different KEK")

	// 4. Opening Tenant A's credential with Tenant B's KEK fails
	_, err = Open(ctx, tenantBKEK, sealedA, aadA)
	require.Error(t, err, "tenant B KEK should not be able to decrypt tenant A credential")
	require.Contains(t, err.Error(), "credential was sealed with a different KEK")

	// 5. Seal for Tenant B
	sealedB, err := multi.SealForTenant(ctx, "tenant-b", secretB, aadB)
	require.NoError(t, err)
	require.Equal(t, tenantBKEK.ID(), sealedB.KEKID)

	openedB, err := Open(ctx, multi, sealedB, aadB)
	require.NoError(t, err)
	require.Equal(t, secretB, openedB)

	// Opening Tenant B's credential with Tenant A's KEK fails
	_, err = Open(ctx, tenantAKEK, sealedB, aadB)
	require.Error(t, err)

	// 6. Tenant with no dedicated KEK falls back to default KEK
	sealedDefault, err := multi.SealForTenant(ctx, "tenant-c", []byte("sk-default"), aadA)
	require.NoError(t, err)
	require.Equal(t, defaultKEK.ID(), sealedDefault.KEKID)

	openedDefault, err := Open(ctx, defaultKEK, sealedDefault, aadA)
	require.NoError(t, err)
	require.Equal(t, []byte("sk-default"), openedDefault)
}
