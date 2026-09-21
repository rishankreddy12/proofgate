package server

import (
	"io"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/mockllm"
	"github.com/stretchr/testify/require"
)

func TestHedgedStreamUsesFasterTarget(t *testing.T) {
	e := setup(t, mockllm.Mode{TTFTMs: 800}, mockllm.Mode{TTFTMs: 20})
	for _, r := range e.h.State.Load().Router.Routes() {
		if r.Name == "default" {
			r.Hedge.Enabled, r.Hedge.MaxExtra = true, 1.0
		}
	}
	start := time.Now()
	resp := e.post(t, "/v1/chat/completions", chat("default", true, "hi"))
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	require.Equal(t, "true", resp.Header.Get("X-ProofGate-Hedged"))
	require.Equal(t, "b/large", resp.Header.Get("X-ProofGate-Target"))
	require.Less(t, time.Since(start), 600*time.Millisecond)
}

func TestHedgedUnaryUsesFasterTarget(t *testing.T) {
	e := setup(t, mockllm.Mode{TTFTMs: 800}, mockllm.Mode{TTFTMs: 20})
	for _, r := range e.h.State.Load().Router.Routes() {
		if r.Name == "default" {
			r.Hedge.Enabled, r.Hedge.MaxExtra = true, 1.0
		}
	}
	start := time.Now()
	resp := e.post(t, "/v1/chat/completions", chat("default", false, "hi"))
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	require.Equal(t, "true", resp.Header.Get("X-ProofGate-Hedged"))
	require.Equal(t, "b/large", resp.Header.Get("X-ProofGate-Target"))
	require.Less(t, time.Since(start), 600*time.Millisecond)
}

