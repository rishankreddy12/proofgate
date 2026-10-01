package server

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP extracts the real client IP address from a request, taking trusted reverse proxies into account.
// If RemoteAddr is not in the trusted proxies list, RemoteAddr is returned (preventing header spoofing).
// If RemoteAddr is in the trusted proxies list, the right-most non-trusted entry in X-Forwarded-For is returned.
func ClientIP(r *http.Request, trusted []*net.IPNet) string {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}
	remoteHost = strings.TrimSpace(remoteHost)
	remoteIP := net.ParseIP(remoteHost)

	// If no trusted proxies configured or remote is not a parseable IP, return RemoteAddr
	if len(trusted) == 0 || remoteIP == nil {
		return remoteHost
	}

	// Verify if RemoteAddr is a trusted proxy
	isTrusted := false
	for _, network := range trusted {
		if network.Contains(remoteIP) {
			isTrusted = true
			break
		}
	}
	if !isTrusted {
		return remoteHost
	}

	// Remote is trusted, parse X-Forwarded-For
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remoteHost
	}

	parts := strings.Split(xff, ",")
	// Traverse from right to left
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		ip := net.ParseIP(candidate)
		if ip == nil {
			continue
		}
		trustedCandidate := false
		for _, network := range trusted {
			if network.Contains(ip) {
				trustedCandidate = true
				break
			}
		}
		if !trustedCandidate {
			return candidate
		}
	}

	// If all were trusted proxies, return leftmost entry
	first := strings.TrimSpace(parts[0])
	if first != "" {
		return first
	}
	return remoteHost
}
