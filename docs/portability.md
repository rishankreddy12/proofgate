# ProofGate Portability & No-Lock-In Guide (D7)

ProofGate is designed from first principles with **zero proprietary lock-in**. You own your data, your encryption keys, your database schemas, and your application code. If you ever decide to stop using ProofGate, leaving is seamless and requires no client application code changes.

---

## The 6 Pillars of Portability

### 1. Standard OpenAI API Surface (Clients Never Change)
All client applications communicate with ProofGate via standard OpenAI-compatible endpoints (`/v1/chat/completions`, `/v1/embeddings`, `/v1/models`). 
- Switching to another gateway or directly to OpenAI, Azure, or vLLM only requires updating the client `baseURL` environment variable.
- No client-side proprietary SDK is ever installed.

### 2. Standard OpenTelemetry Telemetry
Metrics, spans, and access logs follow open standards:
- Metrics are exposed in standard Prometheus text format on the admin port (`/metrics`).
- Traces and spans are emitted via standard OpenTelemetry HTTP/gRPC exporters.
- Works natively with Datadog, Grafana Mimir/Tempo, Dynatrace, Honeycomb, and AWS CloudWatch.

### 3. Customer-Managed Data Stores
ProofGate runs on your own standard relational and time-series infrastructure:
- **PostgreSQL**: Tenants, API keys, and envelope-encrypted credentials.
- **Redis**: Rate limiters, ephemeral session state, and semantic cache vector indices.
- **ClickHouse**: High-throughput usage audit events and judge evaluations.
All database migrations are standard SQL files located in `internal/store/migrations` and `internal/analytics/migrations`.

### 4. Zero-Trust Envelope Encryption with Your KEK
Provider keys are not locked in an opaque proprietary vault:
- Sealed records use industry-standard **AES-256-GCM** with unique 32-byte Data Encryption Keys (DEK).
- DEKs are encrypted with your Key Encryption Key (KEK).
- You can decrypt any credential offline using standard OpenSSL or Go cryptography.

### 5. Instant Data & Configuration Export
Two built-in CLI commands allow full export of your gateway state:

#### Export Audit & Usage Records
Stream historical usage events from ClickHouse to JSON-Lines (`.jsonl`):
```bash
proofgatectl export usage --since 2026-01-01 --out usage-export.jsonl
```

#### Export Sanitized Configuration Snapshot
Dump the active routing, pricing, and guardrail configuration with all secrets safely redacted:
```bash
proofgatectl export config --config deploy/proofgate.yaml --out config-snapshot.yaml
```

### 6. Apache-2.0 Open-Source License
ProofGate is released under the permissive Apache-2.0 License. There are no "open-core" paywalls, no proprietary features locked behind enterprise cloud licenses, and no forced telemetry beacons.

---

## Migration Matrix

If you need to migrate from ProofGate to another solution, here is the exact change required:

| Target Destination | Client Code Changes | Gateway Configuration Mapping | Credential Migration |
| :--- | :--- | :--- | :--- |
| **Direct to Provider** (e.g. OpenAI) | Update `baseURL` to `https://api.openai.com/v1` | N/A | Use the decrypted provider API key directly in client env |
| **LiteLLM Proxy** | Update `baseURL` to LiteLLM port (e.g. `http://localhost:4000/v1`) | Map `routes.targets` in `proofgate.yaml` to `model_list` in `litellm.config.yaml` | Add provider keys to `litellm_params.api_key` |
| **Bifrost** | Update `baseURL` to Bifrost port (e.g. `http://localhost:8080/v1`) | Map routes to `providers.openai.keys` in `bifrost.config.json` | Add keys to `bifrost.config.json` |
| **Self-Hosted vLLM / Ollama** | Update `baseURL` to local inference server URL | Direct model alias mapping | None (no external API keys required) |

---

## Offline Credential Decryption Example

To demonstrate that you are never locked out of your sealed credentials, here is a pure Go snippet decrypting a Postgres-sealed provider key using your 32-byte KEK:

```go
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
)

// Standard AES-256-GCM decrypt with AAD binding "proofgate:provider:<name>"
func DecryptOffline(sealedCiphertext, encryptedDEK, kekBase64, provider string) (string, error) {
	kek, _ := base64.StdEncoding.DecodeString(kekBase64)
	dekBlock, _ := aes.NewCipher(kek)
	dekGCM, _ := cipher.NewGCM(dekBlock)
	
	encDEKBytes, _ := base64.StdEncoding.DecodeString(encryptedDEK)
	dekNonce, encDEKPayload := encDEKBytes[:12], encDEKBytes[12:]
	dek, err := dekGCM.Open(nil, dekNonce, encDEKPayload, nil)
	if err != nil {
		return "", err
	}
	
	recBlock, _ := aes.NewCipher(dek)
	recGCM, _ := cipher.NewGCM(recBlock)
	ctBytes, _ := base64.StdEncoding.DecodeString(sealedCiphertext)
	recNonce, ctPayload := ctBytes[:12], ctBytes[12:]
	aad := []byte("proofgate:provider:" + provider)
	plaintext, err := recGCM.Open(nil, recNonce, ctPayload, aad)
	return string(plaintext), err
}
```
