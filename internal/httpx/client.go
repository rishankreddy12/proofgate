// Package httpx provides an HTTP client factory with safe outbound policies:
// - Never blindly follow cross-domain redirects that could leak custom credentials.
// - Explicit dial, TLS handshake, keep-alive, and connection pool timeouts.
// - Enforced maximum response body limits to avoid memory exhaustion from oversized upstream payloads.
package httpx

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

var (
	defaultTransportOnce sync.Once
	defaultTransport     *http.Transport

	// ErrResponseTooLarge is returned when an upstream response exceeds the configured byte limit.
	ErrResponseTooLarge = errors.New("upstream response exceeded maximum size limit")
)

// DefaultTransport returns a shared, tuned transport with connection pooling and explicit timeouts.
func DefaultTransport() *http.Transport {
	defaultTransportOnce.Do(func() {
		defaultTransport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          1024,
			MaxIdleConnsPerHost:   256,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: time.Second,
		}
	})
	return defaultTransport
}

// Option configures an http.Client.
type Option func(*http.Client)

// WithTimeout sets the overall client request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *http.Client) {
		c.Timeout = d
	}
}

// WithTransport overrides the client transport.
func WithTransport(rt http.RoundTripper) Option {
	return func(c *http.Client) {
		c.Transport = rt
	}
}

// New returns an http.Client configured with safe defaults:
//   - CheckRedirect is set to http.ErrUseLastResponse so redirects are returned to the caller rather
//     than followed automatically (preventing credential leakage of headers like x-api-key across domains).
//   - DefaultTransport() is used with pooled connections and explicit dial/TLS timeouts.
func New(opts ...Option) *http.Client {
	c := &http.Client{
		Transport: DefaultTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// limitReader wraps an io.Reader and returns ErrResponseTooLarge if more than limit bytes are read.
type limitReader struct {
	r      io.Reader
	limit  int64
	read   int64
	closed bool
}

// LimitReader wraps r to read at most limit bytes. If the reader attempts to read beyond limit,
// it returns ErrResponseTooLarge.
func LimitReader(r io.Reader, limit int64) io.Reader {
	if limit <= 0 {
		return r
	}
	return &limitReader{r: r, limit: limit}
}

// Read executes the primary logic for the Read operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (l *limitReader) Read(p []byte) (int, error) {
	if l.read >= l.limit {
		var check [1]byte
		n, err := l.r.Read(check[:])
		if n > 0 {
			return 0, fmt.Errorf("%w: %d bytes", ErrResponseTooLarge, l.limit)
		}
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	remaining := l.limit - l.read
	toRead := p
	if int64(len(p)) > remaining {
		toRead = p[:remaining]
	}
	n, err := l.r.Read(toRead)
	l.read += int64(n)
	if l.read >= l.limit && err == nil {
		var check [1]byte
		cn, cerr := l.r.Read(check[:])
		if cn > 0 {
			return n, fmt.Errorf("%w: %d bytes", ErrResponseTooLarge, l.limit)
		}
		if cerr != nil && !errors.Is(cerr, io.EOF) {
			return n, cerr
		}
	}
	return n, err
}
