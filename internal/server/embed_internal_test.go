package server

import (
	"context"
	"testing"

	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/stretchr/testify/require"
)

func TestEmbedInternal(t *testing.T) {
	e := setup(t, mockllm.Mode{}, mockllm.Mode{})
	vecs, usage, target, err := e.h.EmbedInternal(context.Background(), "embed", []string{"a b", "c"})
	require.NoError(t, err)
	require.Len(t, vecs, 2)
	require.Len(t, vecs[0], 256)
	require.Equal(t, 3, usage.PromptTokens)
	require.Equal(t, "a/emb", target.String())
}
