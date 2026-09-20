package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/stretchr/testify/require"
)

func TestEventFromCall(t *testing.T) {
	c := pipeline.NewCall(auth.Principal{TenantID: "t1", KeyID: "k1"}, &api.ChatRequest{Stream: true}, &router.Route{Name: "default"})
	c.Target = router.Target{Provider: "a", Model: "m"}
	c.Attempts = 2
	c.Usage = api.Usage{PromptTokens: 10, CompletionTokens: 4, PromptTokensDetails: &api.PromptTokensDetails{CachedTokens: 3}}
	c.CostMicros = 99
	c.Latency, c.UpstreamTime, c.TTFT = 120*time.Millisecond, 117*time.Millisecond, 40*time.Millisecond
	c.Values["cache.saved_micros"] = int64(500)
	c.Values["usage.estimated"] = true

	e := EventFromCall(c)
	require.Equal(t, "t1", e.TenantID)
	require.Equal(t, "a/m", e.Target)
	require.Equal(t, "chat", e.Kind)
	require.Equal(t, "ok", e.Status)
	require.True(t, e.Stream)
	require.EqualValues(t, 3, e.CachedTokens)
	require.EqualValues(t, 500, e.SavedMicros)
	require.EqualValues(t, 3000, e.OverheadUs)
	require.EqualValues(t, 40, e.TTFTMs)
	require.True(t, e.UsageEstimated)
}

func TestUsageStageEmits(t *testing.T) {
	var got []UsageEvent
	s := UsageStage(func(e UsageEvent) bool { got = append(got, e); return true })
	c := pipeline.NewCall(auth.Principal{TenantID: "t"}, &api.ChatRequest{}, &router.Route{Name: "r"})
	s.After(context.Background(), c)
	require.Len(t, got, 1)
	require.Equal(t, c.ID, got[0].RequestID)
}
