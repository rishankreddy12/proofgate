package provider

import (
	"net/http"

	"github.com/proofgate/proofgate/internal/httpx"
)

// Transport returns one tuned transport shared by all adapters. There is no client-wide timeout because
// streams can run for minutes; route timeouts are applied through contexts.
func Transport() *http.Transport {
	return httpx.DefaultTransport()
}

func newClient() *http.Client { return httpx.New() }

