package health

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/proofgate/proofgate/internal/router"
)

// Gossip handles UDP-based cross-replica health metrics exchange.
type Gossip struct {
	addr     string
	peers    []string
	tracker  *Tracker
	interval time.Duration

	mu    sync.Mutex
	conn  *net.UDPConn
	stop  chan struct{}
	wg    sync.WaitGroup
	peersMu sync.RWMutex
}

// NewGossip creates a new cross-replica gossip instance.
func NewGossip(addr string, peers []string, tracker *Tracker, interval time.Duration) *Gossip {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &Gossip{
		addr:     addr,
		peers:    peers,
		tracker:  tracker,
		interval: interval,
		stop:     make(chan struct{}),
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

func (g *Gossip) listen() {
	defer g.wg.Done()
	buf := make([]byte, 65536)

	for {
		select {
		case <-g.stop:
			return
		default:
		}

		n, _, err := g.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-g.stop:
				return
			default:
				continue
			}
		}
		if n > 0 {
			_ = g.MergeMessage(buf[:n])
		}
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
func (g *Gossip) MergeMessage(data []byte) error {
	if g.tracker == nil || len(data) == 0 {
		return nil
	}

	trimmed := strings.TrimSpace(string(data))
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
				g.tracker.Merge(tg, m.Stats)
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
		ttft, _ := strconv.ParseFloat(parts[1], 64)
		dev, _ := strconv.ParseFloat(parts[2], 64)
		tps, _ := strconv.ParseFloat(parts[3], 64)
		errRate, _ := strconv.ParseFloat(parts[4], 64)
		ttftN, _ := strconv.Atoi(parts[5])
		tpsN, _ := strconv.Atoi(parts[6])
		errN, _ := strconv.Atoi(parts[7])
		deg := parts[8] == "1" || strings.EqualFold(parts[8], "true")

		g.tracker.Merge(tg, Stats{
			TTFTMs:    ttft,
			TTFTDevMs: dev,
			TPS:       tps,
			ErrRate:   errRate,
			TTFTN:     ttftN,
			TPSN:      tpsN,
			ErrN:      errN,
			Degraded:  deg,
		})
	}
	return nil
}
