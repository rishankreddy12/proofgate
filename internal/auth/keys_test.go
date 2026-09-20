package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateKey(t *testing.T) {
	k, prefix, hash, err := GenerateKey("live")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(k, "pg_live_"))
	require.Len(t, k, len("pg_live_")+43)
	require.Equal(t, k[:12], prefix)
	require.Equal(t, HashKey(k), hash)
	k2, _, _, _ := GenerateKey("live")
	require.NotEqual(t, k, k2)
}

func TestCanUseRoute(t *testing.T) {
	require.True(t, Principal{}.CanUseRoute("any"))
	p := Principal{AllowedRoutes: []string{"default"}}
	require.True(t, p.CanUseRoute("default"))
	require.False(t, p.CanUseRoute("premium"))
}
