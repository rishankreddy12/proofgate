package health

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/config"
	"github.com/proofgate/proofgate/internal/router"
	"github.com/stretchr/testify/require"
)

func TestGossipExchangeAndMerge(t *testing.T) {
	now := time.Unix(0, 0)
	tg := router.Target{Provider: "openai", Model: "gpt-4o"}

	tr1 := NewTracker(config.HealthConfig{Alpha: 0.5, Breaches: 2, Recover: 30 * time.Second, MinSamples: 2},
		map[string]config.SLO{"openai/gpt-4o": {TTFTMs: 300, MinTPS: 20, MaxErrorRate: 0.5}}, func() time.Time { return now })
	tr2 := NewTracker(config.HealthConfig{Alpha: 0.5, Breaches: 2, Recover: 30 * time.Second, MinSamples: 2},
		map[string]config.SLO{"openai/gpt-4o": {TTFTMs: 300, MinTPS: 20, MaxErrorRate: 0.5}}, func() time.Time { return now })

	g1 := NewGossip("127.0.0.1:0", nil, tr1, 100*time.Millisecond)
	require.NoError(t, g1.Start())
	defer g1.Stop()

	g2 := NewGossip("127.0.0.1:0", nil, tr2, 100*time.Millisecond)
	require.NoError(t, g2.Start())
	defer g2.Stop()

	g1.AddPeer(g2.LocalAddr().String())
	g2.AddPeer(g1.LocalAddr().String())

	// tr1 observes some high latency samples (degrading the target)
	for i := 0; i < 4; i++ {
		tr1.Observe(Sample{Target: tg, TTFT: 1000 * time.Millisecond, Outcome: OK})
	}
	require.True(t, tr1.Degraded(tg), "tr1 should be degraded")
	require.False(t, tr2.Degraded(tg), "tr2 has not observed anything yet")

	// Trigger broadcast from g1
	require.NoError(t, g1.BroadcastOnce())

	// Wait briefly for UDP packet delivery and processing
	require.Eventually(t, func() bool {
		s := tr2.Stats(tg)
		return s.TTFTN > 0 && s.TTFTMs > 0
	}, 2*time.Second, 10*time.Millisecond, "tr2 should receive gossip from tr1")

	// Verify tr2 received degraded status and merged stats
	require.True(t, tr2.Degraded(tg), "tr2 should now recognize degradation via gossip")
	s2 := tr2.Stats(tg)
	require.InDelta(t, 1000.0, s2.TTFTMs, 1e-1)
}

func TestGossipHMACVerification(t *testing.T) {
	now := time.Now()
	tg := router.Target{Provider: "openai", Model: "gpt-4o"}
	secret := "super-secure-secret-key-12345"

	tr1 := NewTracker(config.HealthConfig{Alpha: 0.5, Breaches: 2, Recover: 30 * time.Second, MinSamples: 2},
		map[string]config.SLO{"openai/gpt-4o": {TTFTMs: 300, MinTPS: 20, MaxErrorRate: 0.5}}, func() time.Time { return now })
	tr2 := NewTracker(config.HealthConfig{Alpha: 0.5, Breaches: 2, Recover: 30 * time.Second, MinSamples: 2},
		map[string]config.SLO{"openai/gpt-4o": {TTFTMs: 300, MinTPS: 20, MaxErrorRate: 0.5}}, func() time.Time { return now })

	g1 := NewGossipWithSecret("127.0.0.1:0", nil, tr1, 100*time.Millisecond, secret)
	require.NoError(t, g1.Start())
	defer g1.Stop()

	g2 := NewGossipWithSecret("127.0.0.1:0", nil, tr2, 100*time.Millisecond, secret)
	require.NoError(t, g2.Start())
	defer g2.Stop()

	g1.AddPeer(g2.LocalAddr().String())
	g2.AddPeer(g1.LocalAddr().String())

	// 1. Authorized signed broadcast succeeds
	for i := 0; i < 4; i++ {
		tr1.Observe(Sample{Target: tg, TTFT: 800 * time.Millisecond, Outcome: OK})
	}
	require.NoError(t, g1.BroadcastOnce())

	require.Eventually(t, func() bool {
		s := tr2.Stats(tg)
		return s.TTFTN > 0 && s.TTFTMs > 0
	}, 2*time.Second, 10*time.Millisecond)

	// 2. Unsigned packet rejected by recipient with secret
	rawPayload := []byte("openai/gpt-4o|999.000|0.000|10.000|0.0000|10|10|0|1\n")
	err := g2.MergeMessage(rawPayload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing signature header")

	// 3. Forged signature rejected
	forgedHeader := fmt.Sprintf("SIG|%d|badhexsignature0123456789abcdef\n", time.Now().Unix())
	forgedPacket := append([]byte(forgedHeader), rawPayload...)
	err = g2.MergeMessage(forgedPacket)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid packet signature")

	// 4. Skewed timestamp (> 30s) rejected
	oldTs := time.Now().Unix() - 45 // 45 seconds old
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d\n", oldTs)))
	mac.Write(rawPayload)
	sig := hex.EncodeToString(mac.Sum(nil))
	expiredPacket := append([]byte(fmt.Sprintf("SIG|%d|%s\n", oldTs, sig)), rawPayload...)

	err = g2.MergeMessage(expiredPacket)
	require.Error(t, err)
	require.Contains(t, err.Error(), "timestamp skew too large")
}

func TestGossipPeerIPFilter(t *testing.T) {
	tr := NewTracker(config.HealthConfig{Alpha: 0.5, Breaches: 2, Recover: 30 * time.Second, MinSamples: 2}, nil, time.Now)
	g := NewGossip("127.0.0.1:0", []string{"10.0.0.1:9090", "192.168.1.50:9090"}, tr, time.Second)

	// Local loopback or other untrusted IP is rejected when peers list is strict
	untrustedAddr := &net.UDPAddr{IP: net.ParseIP("172.16.0.5"), Port: 9090}
	require.False(t, g.isAllowedPeer(untrustedAddr))

	// Allowed peer IP is accepted
	trustedAddr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 54321}
	require.True(t, g.isAllowedPeer(trustedAddr))
}

func FuzzMergeMessage(f *testing.F) {
	tr := NewTracker(config.HealthConfig{Alpha: 0.5, Breaches: 2, Recover: 30 * time.Second, MinSamples: 2}, nil, time.Now)
	g := NewGossip("127.0.0.1:0", nil, tr, time.Second)

	// Seeds
	f.Add([]byte(""))
	f.Add([]byte("openai/gpt-4o|100.0|10.0|50.0|0.05|10|10|1|0\n"))
	f.Add([]byte("SIG|123456789|aabbccdd\nopenai/gpt-4o|100.0|10.0|50.0|0.05|10|10|1|0\n"))
	f.Add([]byte(`[{"target":"openai/gpt-4o","ttft_ms":100.0,"tokens_per_sec":50.0,"error_rate":0.01,"ttft_n":5}]`))
	f.Add([]byte(`{"target":"openai/gpt-4o","ttft_ms":-999.0,"tokens_per_sec":-10.0,"error_rate":5.0}`))
	f.Add([]byte("corrupted||||NaN|Inf|-1000|0|bad\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Fuzzing ensures MergeMessage never panics regardless of input bytes
		_ = g.MergeMessage(data)
	})
}
