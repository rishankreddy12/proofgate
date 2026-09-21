package secrets

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type memCreds struct {
	m     map[string]Sealed
	reads int
}

func (c *memCreds) ActiveCredential(_ context.Context, p string) (Sealed, int, error) {
	c.reads++
	return c.m[p], 1, nil
}

func TestKeyCache(t *testing.T) {
	ctx := context.Background()
	k := kek(t)
	s, _ := Seal(ctx, k, []byte("sk-1"), AAD("openai"))
	src := &memCreds{m: map[string]Sealed{"openai": s}}
	var registered []string
	now := time.Unix(0, 0)
	kc := NewKeyCache(src, k, time.Minute, func(v string) { registered = append(registered, v) })
	kc.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		v, err := kc.Get(ctx, "openai")
		require.NoError(t, err)
		require.Equal(t, "sk-1", v)
	}
	require.Equal(t, 1, src.reads)
	require.Equal(t, []string{"sk-1"}, registered)

	now = now.Add(61 * time.Second)
	_, _ = kc.Get(ctx, "openai")
	require.Equal(t, 2, src.reads, "expired entries are re-read and re-decrypted")
}
