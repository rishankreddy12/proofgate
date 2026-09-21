package secrets

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func kek(t *testing.T) *LocalKEK {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	k, err := NewLocalKEK(b)
	require.NoError(t, err)
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	ctx := context.Background()
	k := kek(t)
	s, err := Seal(ctx, k, []byte("sk-live-123"), AAD("openai"))
	require.NoError(t, err)
	require.Equal(t, k.ID(), s.KEKID)
	require.NotContains(t, string(s.Ciphertext), "sk-live")
	require.NotContains(t, string(s.WrappedDEK), "sk-live")

	pt, err := Open(ctx, k, s, AAD("openai"))
	require.NoError(t, err)
	require.Equal(t, "sk-live-123", string(pt))

	s2, _ := Seal(ctx, k, []byte("sk-live-123"), AAD("openai"))
	require.NotEqual(t, s.Ciphertext, s2.Ciphertext, "fresh DEK and nonce every time")
}

func TestOpenRejectsTamperingAndSwaps(t *testing.T) {
	ctx := context.Background()
	k := kek(t)
	s, _ := Seal(ctx, k, []byte("secret"), AAD("openai"))

	_, err := Open(ctx, k, s, AAD("anthropic"))
	require.Error(t, err, "a record cannot be moved to another provider")

	bad := s
	bad.Ciphertext = append([]byte(nil), s.Ciphertext...)
	bad.Ciphertext[0] ^= 1
	_, err = Open(ctx, k, bad, AAD("openai"))
	require.Error(t, err)

	_, err = Open(ctx, kek(t), s, AAD("openai"))
	require.ErrorContains(t, err, "different KEK")
}

func TestRewrap(t *testing.T) {
	ctx := context.Background()
	oldK, newK := kek(t), kek(t)
	s, _ := Seal(ctx, oldK, []byte("secret"), AAD("gemini"))
	r, err := Rewrap(ctx, oldK, newK, s)
	require.NoError(t, err)
	require.Equal(t, s.Ciphertext, r.Ciphertext, "only the DEK is rewrapped")
	pt, err := Open(ctx, newK, r, AAD("gemini"))
	require.NoError(t, err)
	require.Equal(t, "secret", string(pt))
}

func TestLocalKEKValidation(t *testing.T) {
	_, err := NewLocalKEK([]byte("short"))
	require.Error(t, err)
	enc, err := GenerateLocalKEK()
	require.NoError(t, err)
	require.Len(t, enc, 44)
}
