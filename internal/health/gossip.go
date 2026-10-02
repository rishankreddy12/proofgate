package health

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/proofgate/proofgate/internal/router"
	"golang.org/x/time/rate"
)

// Gossip handles UDP-based cross-replica health metrics exchange.
type Gossip struct {
	addr     string
	peers    []string
	tracker  *Tracker
	interval time.Duration
	secret   string

	mu      sync.Mutex
	conn    *net.UDPConn
	stop    chan struct{}
	wg      sync.WaitGroup
	peersMu sync.RWMutex

	rateMu   sync.Mutex
	limiters map[string]*rate.Limiter
}

// NewGossip creates a new cross-replica gossip instance without authentication.
func NewGossip(addr string, peers []string, tracker *Tracker, interval time.Duration) *Gossip {
	return NewGossipWithSecret(addr, peers, tracker, interval, "")
}

// NewGossipWithSecret creates a cross-replica gossip instance secured with HMAC-SHA256.
func NewGossipWithSecret(addr string, peers []string, tracker *Tracker, interval time.Duration, secret string) *Gossip {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &Gossip{
		addr:     addr,
		peers:    peers,
		tracker:  tracker,
		interval: interval,
		secret:   secret,
		stop:     make(chan struct{}),
		limiters: make(map[string]*rate.Limiter),
	}
}

// AddPeer dynamically adds a peer address to the gossip group (useful for testing or dynamic discovery).
func (g *Gossip) AddPeer(peer string) {
	g.peersMu.Lock()
	defer g.peersMu.Unlock()
	for _, p := range g.peers {
		if p == peer {
			return
		}
	}
	g.peers = append(g.peers, peer)
}

// LocalAddr returns the bound network address of the listener, or nil if not running.
func (g *Gossip) LocalAddr() net.Addr {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.conn != nil {
		return g.conn.LocalAddr()
	}
	return nil
}

// Start binds to the configured UDP address and starts listening and broadcasting.
func (g *Gossip) Start() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.conn != nil {
		return errors.New("gossip already started")
	}

	laddr, err := net.ResolveUDPAddr("udp", g.addr)
	if err != nil {
		return fmt.Errorf("resolve gossip addr %q: %w", g.addr, err)
	}

	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return fmt.Errorf("listen gossip udp on %q: %w", g.addr, err)
	}
	g.conn = conn

	g.wg.Add(2)
	go g.listen()
	go g.broadcastLoop()
	return nil
}

// Stop terminates background gossip listening and broadcasting.
func (g *Gossip) Stop() {
	g.mu.Lock()
	if g.conn == nil {
		g.mu.Unlock()
		return
	}
	close(g.stop)
	_ = g.conn.Close()
	g.mu.Unlock()

	g.wg.Wait()
}

func (g *Gossip) allowIP(ip string) bool {
	g.rateMu.Lock()
	defer g.rateMu.Unlock()
	lim, ok := g.limiters[ip]
	if !ok {
		if len(g.limiters) > 1024 {
			g.limiters = make(map[string]*rate.Limiter)
		}
		// 50 packets/sec with burst of 100
		lim = rate.NewLimiter(50, 100)
		g.limiters[ip] = lim
	}
	return lim.Allow()
}

func (g *Gossip) isAllowedPeer(addr *net.UDPAddr) bool {
	if addr == nil {
		return false
	}
	g.peersMu.RLock()
	peers := append([]string(nil), g.peers...)
	g.peersMu.RUnlock()

	if len(peers) == 0 {
		return true
	}

	incomingIP := addr.IP
	for _, p := range peers {
		host, _, err := net.SplitHostPort(p)
		if err != nil {
			host = p
		}
		if pip := net.ParseIP(host); pip != nil {
			if pip.Equal(incomingIP) {
				return true
			}
			if pip.IsLoopback() && incomingIP.IsLoopback() {
				return true
			}
		} else {
			if ips, err := net.LookupIP(host); err == nil {
				for _, ip := range ips {
					if ip.Equal(incomingIP) {
						return true
					}
				}
			}
		}
	}
	return false
}

func (g *Gossip) listen() {
	defer g.wg.Done()
	buf := make([]byte, 65536)

	for {
		select {
		case <-g.stop:
			return
		default:
		}

		n, raddr, err := g.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-g.stop:
				return
			default:
				continue
			}
		}
		if n <= 0 {
			continue
		}

		// 1. Peer IP filtering
		if !g.isAllowedPeer(raddr) {
			continue
		}

		// 2. Per-IP rate limiting
		remoteIP := ""
		if raddr != nil {
			remoteIP = raddr.IP.String()
		}
		if !g.allowIP(remoteIP) {
			continue
		}

		_ = g.MergeMessage(buf[:n])
	}
}

func (g *Gossip) broadcastLoop() {
	defer g.wg.Done()
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()

	for {
		select {
		case <-g.stop:
			return
		case <-ticker.C:
			_ = g.BroadcastOnce()
		}
	}
}

// BroadcastOnce generates and sends a metrics datagram to all configured peers.
func (g *Gossip) BroadcastOnce() error {
	if g.tracker == nil {
		return nil
	}
	snap := g.tracker.Snapshot()
	if len(snap) == 0 {
		return nil
	}

	var b bytes.Buffer
	for tg, s := range snap {
		if s.TTFTN == 0 && s.TPSN == 0 && s.ErrN == 0 && !s.Degraded {
			continue
		}
		deg := "0"
		if s.Degraded {
			deg = "1"
		}
		fmt.Fprintf(&b, "%s|%.3f|%.3f|%.3f|%.4f|%d|%d|%d|%s\n",
			tg, s.TTFTMs, s.TTFTDevMs, s.TPS, s.ErrRate, s.TTFTN, s.TPSN, s.ErrN, deg)
	}

	payload := b.Bytes()
	if len(payload) == 0 {
		return nil
	}

	// Sign payload with HMAC-SHA256 and unix timestamp if secret is configured
	if g.secret != "" {
		now := time.Now().Unix()
		mac := hmac.New(sha256.New, []byte(g.secret))
		mac.Write([]byte(fmt.Sprintf("%d\n", now)))
		mac.Write(payload)
		sig := hex.EncodeToString(mac.Sum(nil))
		header := fmt.Sprintf("SIG|%d|%s\n", now, sig)
		payload = append([]byte(header), payload...)
	}

	g.peersMu.RLock()
	peers := append([]string(nil), g.peers...)
	g.peersMu.RUnlock()

	g.mu.Lock()
	conn := g.conn
	g.mu.Unlock()

	if conn == nil {
		return errors.New("gossip connection not active")
	}

	for _, peer := range peers {
		raddr, err := net.ResolveUDPAddr("udp", peer)
		if err != nil {
			continue
		}
		_, _ = conn.WriteToUDP(payload, raddr)
	}
	return nil
}

// MergeMessage parses incoming gossip packets and applies them to the local tracker.
// Validates HMAC signatures and timestamp skew (<= 30s) if a secret is configured.
// Rejects malformed, non-finite (NaN/Inf), or negative/excessive metric values.
func (g *Gossip) MergeMessage(data []byte) error {
	if g.tracker == nil || len(data) == 0 {
		return nil
	}

	raw := data
	if g.secret != "" {
		nlIdx := bytes.IndexByte(raw, '\n')
		if nlIdx < 0 {
			return errors.New("gossip: packet missing signature header")
		}
		headerLine := string(raw[:nlIdx])
		parts := strings.Split(headerLine, "|")
		if parts[0] != "SIG" {
			return errors.New("gossip: packet missing signature header")
		}
		if len(parts) != 3 {
			return errors.New("gossip: packet invalid signature header format")
		}
		ts, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return errors.New("gossip: invalid timestamp in signature header")
		}
		now := time.Now().Unix()
		diff := now - ts
		if diff < -30 || diff > 30 {
			return fmt.Errorf("gossip: timestamp skew too large (%d s)", diff)
		}
		expectedMac := hmac.New(sha256.New, []byte(g.secret))
		expectedMac.Write([]byte(fmt.Sprintf("%d\n", ts)))
		expectedMac.Write(raw[nlIdx+1:])
		expectedSig := expectedMac.Sum(nil)
		actualSig, err := hex.DecodeString(parts[2])
		if err != nil || !hmac.Equal(expectedSig, actualSig) {
			return errors.New("gossip: invalid packet signature")
		}
		raw = raw[nlIdx+1:]
	} else if bytes.HasPrefix(raw, []byte("SIG|")) {
		if nlIdx := bytes.IndexByte(raw, '\n'); nlIdx >= 0 {
			raw = raw[nlIdx+1:]
		}
	}

	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 0 {
		return nil
	}

	// Handle JSON packet if payload starts with '{' or '['
	if trimmed[0] == '{' || trimmed[0] == '[' {
		var msgs []struct {
			Target string `json:"target"`
			Stats
		}
		if trimmed[0] == '{' {
			var single struct {
				Target string `json:"target"`
				Stats
			}
			if err := json.Unmarshal([]byte(trimmed), &single); err == nil {
				msgs = append(msgs, single)
			}
		} else {
			_ = json.Unmarshal([]byte(trimmed), &msgs)
		}

		for _, m := range msgs {
			if tg, ok := router.ParseTarget(m.Target); ok {
				if validateStats(m.Stats) {
					g.tracker.Merge(tg, m.Stats)
				}
			}
		}
		return nil
	}

	// Delimiter-separated compact line format
	lines := strings.Split(trimmed, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 9 {
			continue
		}
		tg, ok := router.ParseTarget(parts[0])
		if !ok {
			continue
		}
		ttft, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			continue
		}
		dev, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			continue
		}
		tps, err := strconv.ParseFloat(parts[3], 64)
		if err != nil {
			continue
		}
		errRate, err := strconv.ParseFloat(parts[4], 64)
		if err != nil {
			continue
		}
		ttftN, err := strconv.Atoi(parts[5])
		if err != nil {
			continue
		}
		tpsN, err := strconv.Atoi(parts[6])
		if err != nil {
			continue
		}
		errN, err := strconv.Atoi(parts[7])
		if err != nil {
			continue
		}
		deg := parts[8] == "1" || strings.EqualFold(parts[8], "true")

		s := Stats{
			TTFTMs:    ttft,
			TTFTDevMs: dev,
			TPS:       tps,
			ErrRate:   errRate,
			TTFTN:     ttftN,
			TPSN:      tpsN,
			ErrN:      errN,
			Degraded:  deg,
		}
		if validateStats(s) {
			g.tracker.Merge(tg, s)
		}
	}
	return nil
}

func validateStats(s Stats) bool {
	if math.IsNaN(s.TTFTMs) || math.IsInf(s.TTFTMs, 0) ||
		math.IsNaN(s.TTFTDevMs) || math.IsInf(s.TTFTDevMs, 0) ||
		math.IsNaN(s.TPS) || math.IsInf(s.TPS, 0) ||
		math.IsNaN(s.ErrRate) || math.IsInf(s.ErrRate, 0) {
		return false
	}
	const maxCount = 1_000_000
	if s.TTFTMs < 0 || s.TTFTDevMs < 0 || s.TPS < 0 || s.ErrRate < 0 || s.ErrRate > 1.0 {
		return false
	}
	if s.TTFTN < 0 || s.TPSN < 0 || s.ErrN < 0 ||
		s.TTFTN > maxCount || s.TPSN > maxCount || s.ErrN > maxCount {
		return false
	}
	return true
}
