//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/proofgate/proofgate/internal/secrets"
	"github.com/stretchr/testify/require"
)

func TestCredentials(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	k, _ := secrets.NewLocalKEK(make([]byte, 32))
	s1, _ := secrets.Seal(ctx, k, []byte("key-1"), secrets.AAD("openai"))
	s2, _ := secrets.Seal(ctx, k, []byte("key-2"), secrets.AAD("openai"))

	v, err := s.PutCredential(ctx, "openai", s1, "alice")
	require.NoError(t, err)
	require.Equal(t, 1, v)
	v, err = s.PutCredential(ctx, "openai", s2, "bob")
	require.NoError(t, err)
	require.Equal(t, 2, v)

	got, ver, err := s.ActiveCredential(ctx, "openai")
	require.NoError(t, err)
	require.Equal(t, 2, ver)
	pt, err := secrets.Open(ctx, k, got, secrets.AAD("openai"))
	require.NoError(t, err)
	require.Equal(t, "key-2", string(pt))

	infos, err := s.ListCredentials(ctx)
	require.NoError(t, err)
	require.Len(t, infos, 2)
	require.False(t, infos[0].Active)
	require.True(t, infos[1].Active)

	_, _, err = s.ActiveCredential(ctx, "gemini")
	require.ErrorIs(t, err, ErrNotFound)
}
