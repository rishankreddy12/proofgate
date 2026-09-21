package server

import (
	"io"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/health"
	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/stretchr/testify/require"
)

func TestSlowTargetIsRoutedAround(t *testing.T) {
	e := setup(t, mockllm.Mode{TTFTMs: 400}, mockllm.Mode{})
	tr := health.NewTracker(config.HealthConfig{Alpha: 0.5, Breaches: 2, Recover: time.Minute, MinSamples: 2},
		map[string]config.SLO{"a/small": {TTFTMs: 150}}, time.Now)
	e.h.Health = tr
	e.h.State.Load().Router.SetHealth(tr.Degraded)

	for i := 0; i < 4; i++ {
		resp := e.post(t, "/v1/chat/completions", chat("default", true, "hi"))
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	require.True(t, tr.Degraded(router.Target{Provider: "a", Model: "small"}))
	resp := e.post(t, "/v1/chat/completions", chat("default", true, "hi"))
	defer resp.Body.Close()
	require.Equal(t, "b/large", resp.Header.Get("X-ProofGate-Target"), "slow, not failed: still routed around")
}
