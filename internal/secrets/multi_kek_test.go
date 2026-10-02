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

func TestMultiKEKRotation(t *testing.T) {
	ctx := context.Background()

	oldKEK1 := newTestLocalKEK()
	oldKEK2 := newTestLocalKEK()
	currentKEK := newTestLocalKEK()

	// MultiKEK holds current default KEK and historical KEKs for rotation
	multi := NewMultiKEK(currentKEK, oldKEK1, oldKEK2)

	secret1 := []byte("sk-historical-key-1")
	secret2 := []byte("sk-historical-key-2")
	secretCurrent := []byte("sk-new-key-current")

	aad := AAD("openai")

	// 1. Credentials sealed in the past under old KEKs
	sealed1, err := Seal(ctx, oldKEK1, secret1, aad)
	require.NoError(t, err)
	sealed2, err := Seal(ctx, oldKEK2, secret2, aad)
	require.NoError(t, err)

	// 2. Open via MultiKEK succeeds by resolving historical KEKs
	opened1, err := Open(ctx, multi, sealed1, aad)
	require.NoError(t, err)
	require.Equal(t, secret1, opened1)

	opened2, err := Open(ctx, multi, sealed2, aad)
	require.NoError(t, err)
	require.Equal(t, secret2, opened2)

	// 3. Opening with only currentKEK fails because it lacks the old KEKs
	_, err = Open(ctx, currentKEK, sealed1, aad)
	require.Error(t, err)
	require.Contains(t, err.Error(), "credential was sealed with a different KEK")

	// 4. Sealing through MultiKEK always uses current default KEK
	sealedNew, err := Seal(ctx, multi, secretCurrent, aad)
	require.NoError(t, err)
	require.Equal(t, currentKEK.ID(), sealedNew.KEKID)

	openedNew, err := Open(ctx, multi, sealedNew, aad)
	require.NoError(t, err)
	require.Equal(t, secretCurrent, openedNew)

	// 5. Rewrap historical credential to current KEK using MultiKEK as source
	rewrapped1, err := Rewrap(ctx, multi, currentKEK, sealed1)
	require.NoError(t, err)
	require.Equal(t, currentKEK.ID(), rewrapped1.KEKID)

	// Now currentKEK alone can open it
	openedRewrapped, err := Open(ctx, currentKEK, rewrapped1, aad)
	require.NoError(t, err)
	require.Equal(t, secret1, openedRewrapped)
}
