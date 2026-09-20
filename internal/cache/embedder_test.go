package cache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLRUEmbedder(t *testing.T) {
	calls := 0
	inner := EmbedFunc(func(_ context.Context, _, _, text string) ([]float32, error) {
		calls++
		return []float32{float32(len(text))}, nil
	})
	e := NewLRUEmbedder(inner, 2)
	ctx := context.Background()
	v, _ := e.Embed(ctx, "embed", "t1", "aa")
	require.Equal(t, []float32{2}, v)
	_, _ = e.Embed(ctx, "embed", "t2", "aa") // different tenant, same text: cached
	require.Equal(t, 1, calls)
	_, _ = e.Embed(ctx, "embed", "t1", "bbb")
	_, _ = e.Embed(ctx, "embed", "t1", "cccc") // evicts "aa"
	_, _ = e.Embed(ctx, "embed", "t1", "aa")
	require.Equal(t, 4, calls)
}
