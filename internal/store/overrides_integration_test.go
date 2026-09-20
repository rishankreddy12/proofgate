//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOverrides(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	require.NoError(t, s.SetOverride(ctx, Override{Route: "faq", Key: "cache.threshold", Value: "0.93", Reason: "tuner", Actor: "system"}))
	require.NoError(t, s.SetOverride(ctx, Override{Route: "faq", Key: "cache.threshold", Value: "0.94", Reason: "tuner", Actor: "system"}))
	ovs, err := s.Overrides(ctx)
	require.NoError(t, err)
	require.Len(t, ovs, 1)
	require.Equal(t, "0.94", ovs[0].Value)

	require.NoError(t, s.ClearOverride(ctx, "faq", "cache.threshold", "admin", "manual"))
	ovs, _ = s.Overrides(ctx)
	require.Empty(t, ovs)

	evs, err := s.ProofEvents(ctx, "faq", 10)
	require.NoError(t, err)
	require.Len(t, evs, 3)
	require.Equal(t, "override_cleared", evs[0].Kind, "newest first")
}
