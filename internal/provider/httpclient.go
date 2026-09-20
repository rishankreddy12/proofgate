package provider

import (
	"net"
	"net/http"
	"sync"
	"time"
)

var (
	transportOnce sync.Once
	transport     *http.Transport
)

// Transport returns one tuned transport shared by all adapters. There is no client-wide timeout because
// streams can run for minutes; route timeouts are applied through contexts.
func Transport() *http.Transport {
	transportOnce.Do(func() {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          1024,
			MaxIdleConnsPerHost:   256,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: time.Second,
		}
	})
	return transport
}

func newClient() *http.Client { return &http.Client{Transport: Transport()} }
