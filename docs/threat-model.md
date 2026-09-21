# ProofGate STRIDE Threat Model

This document specifies the threat model for ProofGate, treating the gateway as the security-critical boundary and single point of failure that it is.

The threat model is machine-checked by tests (`internal/securitydocs/threatmodel_test.go`): every control listed below is verified by an automated test in the repository.

---

## Scope & Trust Boundaries

ProofGate acts as a reverse proxy sitting between client applications, upstream LLM providers (OpenAI, Anthropic, Gemini, Ollama), third-party Model Context Protocol (MCP) servers, and data stores (Postgres, Redis, ClickHouse, HashiCorp Vault).

```
+-----------------------------------------------------------------------------------+
| CLIENT DOMAIN                                                                     |
| Applications / Agents (authenticate via ProofGate API keys pg_live_*)              |
+----------------------------------------+------------------------------------------+
                                         | TLS + Bearer Auth + Rate Limit + Guard
                                         v
+-----------------------------------------------------------------------------------+
| PROOFGATE GATEWAY ENCLAVE (Non-root, read-only, no capabilities, distroless)       |
|                                                                                   |
|  [Ingress]                                                                        |
|    │                                                                              |
|    ├──► Auth Middleware (Tenant & Key validation, hash verification)              |
|    ├──► Guardrails (Prompt injection screening, PII redaction)                   |
|    ├──► Agent Governance (Per-run budget, step & token caps, loop detection)      |
|    ├──► Safe Semantic Cache (Tenant & scope isolated vector match)               |
|    ├──► SLO-Aware Router & Hedger (Dynamic delay race,loser cancellation)         |
|    │                                                                              |
|  [Key Management]                                                                 |
|    └──► Secrets Subsystem (AES-256-GCM envelope encryption, 60s memory cache)     |
|                                                                                   |
|  [Telemetry & Audit]                                                              |
|    └──► Scrubber (Log & panic handler redacting all provider & gateway secrets)   |
+-------------------+--------------------+-------------------+----------------------+
                    |                    |                   |
                    v                    v                   v
            +---------------+    +---------------+   +---------------+
            |  POSTGRESQL   |    |  REDIS STACK  |   |  CLICKHOUSE   |
            | Sealed creds, |    | Limits, runs, |   | Audit trail,  |
            | keys, tenants |    | cache vectors |   | shadow events |
            +---------------+    +---------------+   +---------------+
                    ^
                    | Transit Unwrap
            +---------------+
            |  HASHICORP    |
            |     VAULT     |
            |  Transit KEK  |
            +---------------+
```

### Trust Boundaries
1. **Clients $\to$ Gateway**: Untrusted input. Clients may attempt prompt injection, credential theft, budget exhaustion, or SSRF attacks.
2. **Gateway $\to$ Upstream Providers & MCP Servers**: Controlled egress. Upstream responses may contain hostile tool calls, reflections, or attempt prompt leakage.
3. **Gateway $\to$ Persistence Layers (Postgres, Redis, ClickHouse)**: Hardened storage boundary. Database dumps must not yield plaintext provider API keys.
4. **CI/CD $\to$ Artifact Registry**: Supply-chain boundary. Compromised CI steps must not be able to poison published gateway images or steal release secrets.

---

## STRIDE Threat Matrix

| ID | STRIDE | Asset | Threat | Mitigation & Controls | Test Verification |
|---|---|---|---|---|---|
| **T1** | Information Disclosure | Provider API keys at rest | Database dump or backup leak exposes provider keys | Envelope encryption (AES-256-GCM per-record DEK, KEK outside database); AAD binds ciphertext to provider | `internal/secrets/envelope_test.go:TestSealOpenRoundTrip`, `internal/secrets/envelope_test.go:TestOpenRejectsTamperingAndSwaps` |
| **T2** | Information Disclosure | Provider API keys in memory | Heap dump or core file of running gateway | Decrypted keys cached at most 60s; DEK byte slices zeroed after use; read-only distroless container | `internal/secrets/keycache_test.go:TestKeyCache` |
| **T3** | Information Disclosure | Secrets in logs | Provider keys or ProofGate API keys appear in logs, errors or traces | Scrubbing slog handler; access log without headers/bodies; panic recovery logs no request data; Vault errors never echo bodies | `internal/telemetry/scrub_test.go:TestScrubber`, `internal/telemetry/telemetry_test.go:TestAccessLogKeepsFlusherAndHidesSecrets`, `internal/server/recover_test.go:TestRecover` |
| **T4** | Tampering | Build and release pipeline | Compromised CI action or dependency injects code | All GitHub Actions pinned by 40-character commit SHA; least-privilege job permissions; `go mod verify` + `govulncheck`; CODEOWNERS | `scripts/tests/test_check_pinned_actions.sh` |
| **T5** | Spoofing | Published container images | Attacker publishes a look-alike or tampered image | Keyless Cosign signatures tied to release workflow identity; Syft SPDX SBOM and SLSA build provenance attestations; zero long-lived publish tokens | `.github/workflows/release.yml` |
| **T6** | Elevation of Privilege | Gateway container | Remote code execution escalates inside container | Distroless base image (no `/bin/sh`); non-root user `65532:65532`; read-only root filesystem; `cap_drop: [ALL]`; `no-new-privileges:true` | `scripts/check_image.sh` |
| **T7** | Spoofing | Tenant API access | Stolen or guessed ProofGate key | 256-bit random keys; SHA-256 hashed at rest; route allow-lists per key | `internal/auth/middleware_test.go:TestMiddleware`, `internal/auth/keys_test.go:TestGenerateKey` |
| **T8** | Information Disclosure | Other tenants' cached answers | Cross-tenant cache leak through semantic vector match | Tenant ID in every cache scope and as a strict pre-filter on vector search | `internal/cache/semantic_integration_test.go:TestSemanticNearestIsTenantAndScopeIsolated`, `internal/cache/keys_test.go:TestScopeSeparatesWhatMatters` |
| **T9** | Tampering | Model behavior and tools | Prompt injection through user input or tool output | Heuristic screening of user and tool messages; judge prompts fence data in `<data>` tags; MCP tool allow-lists (deny by default) | `internal/guard/stage_test.go:TestGuardStage_Injection_Block`, `internal/mcpproxy/proxy_test.go:TestDeniedCallNeverReachesServer` |
| **T10** | Information Disclosure | MCP credentials | Gateway client key forwarded to third-party MCP server | Proxy never copies client `Authorization` header; upstream credentials come strictly from configuration | `internal/mcpproxy/proxy_test.go:TestToolsListIsFilteredJSONAndSSE` |
| **T11** | Denial of Service | Gateway availability and spend | Runaway agents or single tenant exhausting capacity or budget | Per-tenant RPM/TPM; monthly budgets; per-run step/cost/token caps; normalized loop detection; shadow budget cap | `internal/ratelimit/limiter_integration_test.go:TestThreeReplicasShareOneLimit`, `internal/agentrun/runs_integration_test.go:TestStepsCostAndLoops`, `internal/proof/labeler_test.go:TestCacheLabelWorker_StratifiedBatch` |
| **T12** | Information Disclosure | Personal data in prompts | PII sent to third-party providers or stored in shadow tables | PII redaction before cache, shadow storage and upstream calls; reversible via stream restorer; 30-day TTL | `internal/guard/redact_test.go:TestPII_RedactAndRestore_Unary`, `internal/guard/stage_test.go:TestGuardStage_PII_UnaryFlow` |
| **T13** | Repudiation | Live routing and cache decisions | Live threshold or routing mode modified without trace | All background changes go through `route_overrides` with an immutable `proof_events` audit row | `internal/store/overrides_integration_test.go:TestOverrides` |

---

## Case Study: The LiteLLM PyPI Compromise (March 2026)

In March 2026, the popular open-source LLM proxy LiteLLM suffered a critical software supply-chain compromise. Attackers gained write access to the PyPI package repository and published malicious releases containing credential-harvesting backdoors.

Below is an analysis of each attack stage and how ProofGate's zero-trust architecture structurally neutralizes it:

| Attack Vector in LiteLLM Compromise | ProofGate Architectural Control |
|---|---|
| **Mutable CI Action Compromise**: Attackers compromised a third-party security scanner action referenced by branch/tag (`@master`), modifying it to extract maintainer PyPI credentials. | **SHA-Pinned Actions & Least Privilege**: Every action in `.github/workflows` is pinned to an immutable 40-character commit SHA verified by `scripts/check_pinned_actions.sh`. Workflows start with `permissions: {}` and grant minimum scoped permissions. |
| **Long-Lived Publishing Token Leakage**: The CI environment held long-lived PyPI API tokens with publishing permissions across releases. | **Zero Long-Lived Publishing Credentials**: Releases use GitHub's short-lived OIDC tokens. Images are published to GHCR using transient `GITHUB_TOKEN`. Container signing uses keyless Cosign via GitHub Actions OIDC identity. |
| **Malicious Package Execution on Startup**: A malicious `.pth` file executed automatically upon Python interpreter initialization, exfiltrating environment variables and credentials. | **Static Binary, No Shell, No Interpreter**: ProofGate is compiled into a single static Go binary (`CGO_ENABLED=0`) running in a minimal Google Distroless image with no Python interpreter, no shell (`/bin/sh`), and no package manager. |
| **Harvesting Provider Keys from Environment Variables**: The backdoor dumped `os.environ` containing `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, etc. | **Envelope Encryption at Rest**: Provider API keys are never stored in environment variables in production. Keys are sealed in Postgres with AES-256-GCM data encryption keys wrapped by a KEK (local or Vault Transit). Plaintext is fetched on demand and cached in memory for at most 60 seconds. |
| **Credential Exfiltration via Egress**: The malware scanned container filesystems for Kubernetes ServiceAccount tokens and cloud credentials, exfiltrating them to an external C2 server. | **Read-Only Container & Zero Cloud Credentials**: Containers run with `read_only: true`, `user: 65532:65532`, `cap_drop: [ALL]`, and `no-new-privileges:true`. Pods hold no cloud credentials. Gateway pods only hold a Vault identity via Kubernetes auth. |
