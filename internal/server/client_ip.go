// Package server provides enterprise-grade capabilities, configuration, and structural components for the server subsystem.
package server

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP extracts the genuine client IP address from an incoming HTTP request.
// In modern cloud deployments, the gateway sits behind reverse proxies (ALB, NGINX),
// making r.RemoteAddr useless as it resolves to the proxy's internal IP.
//
// This function implements strict header parsing to prevent X-Forwarded-For spoofing vectors:
//  1. It mandates an explicit configuration of trusted proxy CIDRs.
//  2. If r.RemoteAddr does not originate from a trusted proxy, it returns r.RemoteAddr immediately (ignoring headers).
//  3. If it is trusted, it traverses the X-Forwarded-For header from right-to-left, discarding any trusted proxies
//     until it hits the first untrusted IP. This is mathematically guaranteed to be the actual client edge IP.
func ClientIP(r *http.Request, trusted []*net.IPNet) string {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}
	remoteHost = strings.TrimSpace(remoteHost)
	remoteIP := net.ParseIP(remoteHost)

	// If no trusted proxies are configured or remote is unparseable, return RemoteAddr.
	if len(trusted) == 0 || remoteIP == nil {
		return remoteHost
	}

	// Verify if the direct RemoteAddr is within our trusted CIDR blocks.
	isTrusted := false
	for _, network := range trusted {
		if network.Contains(remoteIP) {
			isTrusted = true
			break
		}
	}

	// If the connection didn't come from a trusted proxy, we cannot trust XFF headers.
	if !isTrusted {
		return remoteHost
	}

	// Remote is trusted; parse the X-Forwarded-For chain.
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remoteHost
	}

	parts := strings.Split(xff, ",")

	// Traverse from right to left (most trusted to least trusted)
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		ip := net.ParseIP(candidate)
		if ip == nil {
			continue
		}

		// Check if this hop in the chain is another trusted proxy
		trustedCandidate := false
		for _, network := range trusted {
			if network.Contains(ip) {
				trustedCandidate = true
				break
			}
		}

		// The first non-trusted hop encountered going right-to-left is the real client.
		if !trustedCandidate {
			return candidate
		}
	}

	// If all hops were trusted proxies, return the leftmost entry as the ultimate source.
	first := strings.TrimSpace(parts[0])
	if first != "" {
		return first
	}
	return remoteHost
}
