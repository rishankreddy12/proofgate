// Package provider provides enterprise-grade capabilities, configuration, and structural components for the provider subsystem.
package provider

import (
	"net/http"

	"github.com/proofgate/proofgate/internal/httpx"
)

// Transport returns a highly-tuned HTTP Transport shared by all LLM API adapters.
//
// Architecture: It disables global client-level read timeouts in favor of request-scoped
// context cancellation, because large generation streams can routinely take minutes to
// complete. Timeouts are enforced at the Route level via the context.
func Transport() *http.Transport {
	return httpx.DefaultTransport()
}

// newClient instantiates a raw HTTP Client bound to the shared, tuned Transport.
func newClient() *http.Client { return httpx.New() }
