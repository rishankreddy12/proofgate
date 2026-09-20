//go:build integration

package cache

import (
	"context"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/stretchr/testify/require"
)

func TestSemanticNearestIsTenantAndScopeIsolated(t *testing.T) {
	s := NewSemantic(redisClient(t))
	ctx := context.Background()
	q := "how do I reset my password"
	emb := mockllm.HashEmbedding(q, 64)
	_, err := s.Put(ctx, "tenant-a", "default", "scope1", emb, Entry{SourceRequestID: "r1", Query: q, Response: resp("go to settings")}, time.Minute)
	require.NoError(t, err)

	m, err := s.Nearest(ctx, "tenant-a", "scope1", mockllm.HashEmbedding("how can I reset my password", 64))
	require.NoError(t, err)
	require.NotNil(t, m)
	require.Equal(t, "r1", m.Entry.SourceRequestID)
	require.Greater(t, m.Similarity, 0.7)
	require.LessOrEqual(t, m.Similarity, 1.0)

	m, err = s.Nearest(ctx, "tenant-b", "scope1", emb)
	require.NoError(t, err)
	require.Nil(t, m, "another tenant must never match")

	m, err = s.Nearest(ctx, "tenant-a", "scope2", emb)
	require.NoError(t, err)
	require.Nil(t, m, "another scope must never match")
}
