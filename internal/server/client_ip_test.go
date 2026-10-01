package server

import (
	"net"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientIP(t *testing.T) {
	_, loopback, _ := net.ParseCIDR("127.0.0.1/32")
	_, priv10, _ := net.ParseCIDR("10.0.0.0/8")
	trusted := []*net.IPNet{loopback, priv10}

	// 1. Untrusted remote peer attempts to spoof X-Forwarded-For
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.25:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.195, 10.0.0.1")
	ip := ClientIP(req, trusted)
	require.Equal(t, "198.51.100.25", ip, "untrusted peer must not be allowed to spoof X-Forwarded-For")

	// 2. No trusted proxies configured: always returns RemoteAddr
	ipNoTrust := ClientIP(req, nil)
	require.Equal(t, "198.51.100.25", ipNoTrust)

	// 3. Trusted reverse proxy forwarding client IP
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "10.0.0.5:54321"
	req2.Header.Set("X-Forwarded-For", "203.0.113.88")
	ip2 := ClientIP(req2, trusted)
	require.Equal(t, "203.0.113.88", ip2)

	// 4. Multi-hop: client -> intermediate untrusted/trusted proxies -> ingress -> gateway
	req3 := httptest.NewRequest("GET", "/", nil)
	req3.RemoteAddr = "10.0.0.1:8080"
	req3.Header.Set("X-Forwarded-For", "198.51.100.1, 203.0.113.44, 10.2.3.4")
	ip3 := ClientIP(req3, trusted)
	require.Equal(t, "203.0.113.44", ip3, "must select the right-most non-trusted entry")

	// 5. All entries in XFF are trusted proxies
	req4 := httptest.NewRequest("GET", "/", nil)
	req4.RemoteAddr = "10.0.0.1:8080"
	req4.Header.Set("X-Forwarded-For", "10.1.1.1, 10.2.2.2")
	ip4 := ClientIP(req4, trusted)
	require.Equal(t, "10.1.1.1", ip4)
}
