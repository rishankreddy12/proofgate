# ProofGate Configuration Reference

This document provides an exhaustive reference for `proofgate.yaml`. All fields are checked by automated regression tests to guarantee documentation parity with the active codebase.

---

## 1. Top-Level Structure

A canonical `proofgate.yaml` is composed of the following top-level blocks:
- `server`: HTTP listener addresses for data plane and control plane.
- `secrets`: Zero-trust Key Encryption Key (KEK) and Vault configuration.
- `providers`: Upstream LLM backend definitions.
- `pricing`: Model cost rates per 1M tokens.
- `routes`: Virtual routing topologies, fallback strategies, caching, and guardrails.
- `defaults`: Global system fallback limits.
- `slos`: Target Service Level Objectives for health and hedging.
- `health`: Exponentially Weighted Moving Average (EWMA) health tracker tuning.
- `mcp_servers`: Model Context Protocol upstream reverse proxy endpoints.
- `mcp_insecure_hosts`: Whitelisted hosts for unencrypted MCP communication.

---

## 2. Server Configuration (`server`)

Controls bind addresses for client traffic and management APIs.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `server` | object | - | Root configuration for server listeners. |
| `addr` | string | `":8080"` | Listen address for public OpenAI-compatible data plane APIs. |
| `admin_addr` | string | `"127.0.0.1:9090"` | Listen address for private management, reload, metrics, and readiness checks. **Never expose to the public internet.** |

```yaml
server:
  addr: ":8080"
  admin_addr: "0.0.0.0:9090"
```

---

## 3. Zero-Trust Secrets (`secrets`)

Controls envelope encryption and Key Encryption Key (KEK) management.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `secrets` | object | - | Root configuration for provider key envelope encryption. |
| `kek` | string | `"local"` | KEK provider type: `"local"` or `"vault"`. |
| `local_kek_file` | string | `""` | Absolute path to 32-byte base64 local KEK file (e.g. `/run/secrets/local_kek`). |
| `vault_addr` | string | `""` | Base URL of HashiCorp Vault server (e.g. `http://vault:8200`). |
| `vault_key` | string | `""` | Name of Vault Transit key used to wrap DEKs. |
| `vault_auth` | string | `"token"` | Vault authentication mode: `"token"` (via `VAULT_TOKEN`) or `"kubernetes"`. |
| `vault_role` | string | `""` | Vault Kubernetes auth role name. |
| `cache_ttl` | duration | `60s` | Maximum TTL for decrypted in-memory provider credentials before automatic eviction. |

```yaml
secrets:
  kek: local
  local_kek_file: /run/secrets/local_kek
  cache_ttl: 60s
```

---

## 4. Upstream Providers (`providers`)

Defines upstream LLM APIs and credentials.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `providers` | list | `[]` | List of upstream provider definitions. |
| `name` | string | - | Unique provider identifier referenced by routes. |
| `type` | string | `"openai"` | Protocol adapter: `"openai"`, `"anthropic"`, or `"gemini"`. |
| `base_url` | string | - | Upstream target base URL (e.g. `https://api.openai.com/v1`). |
| `api_key_env` | string | `""` | Environment variable name storing provider API key (e.g. `OPENAI_API_KEY`). |
| `api_key_db` | boolean | `false` | When `true`, dynamically fetches envelope-encrypted key from PostgreSQL. |
| `headers` | map | `{}` | Static HTTP headers sent with every upstream request. |

```yaml
providers:
  - name: openai-prod
    type: openai
    base_url: https://api.openai.com/v1
    api_key_db: true
    headers:
      OpenAI-Organization: org-enterprise-123
```

---

## 5. Pricing Catalog (`pricing`)

Defines token pricing for exact cost tracking and counterfactual savings estimation (USD per 1M tokens).

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `pricing` | map | `{}` | Keyed by `provider/model` string (e.g. `openai-prod/gpt-4o`). |
| `input` | float | `0.0` | Cost in USD per 1M unprompted input tokens. |
| `output` | float | `0.0` | Cost in USD per 1M generated completion tokens. |
| `cached_input` | float | `0.0` | Cost in USD per 1M cached prompt tokens (for providers with prompt caching). |

```yaml
pricing:
  openai-prod/gpt-4o-mini:
    input: 0.15
    output: 0.60
    cached_input: 0.075
```

---

## 6. Routes & Pipeline Topology (`routes`)

Virtual routes define client model aliases, failover, caching, and guardrails.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `routes` | list | - | List of route definitions. |
| `name` | string | - | Route identifier matching client request `model`. |
| `targets` | list | - | Priority-ordered upstream targets. |
| `provider` | string | - | Name of provider defined under `providers`. |
| `model` | string | - | Upstream model identifier sent in provider payload. |
| `strategy` | string | `"fallback"` | Target routing strategy: `"fallback"` or `"round-robin"`. |
| `retry` | object | - | Retry policy for failed attempts. |
| `max_attempts` | integer | `3` | Maximum number of upstream retry attempts across targets. |
| `base_delay` | duration | `100ms` | Initial exponential backoff delay before retries. |
| `timeout` | duration | `120s` | Overall per-request execution timeout. |
| `stream_idle_timeout` | duration | `15s` | Timeout between streaming chunks before aborting stalled streams. |
| `embeddings` | boolean | `false` | When `true`, route accepts `/v1/embeddings` requests instead of chat. |

---

## 7. Caching (`cache`)

Configured per route under `routes[].cache`.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `cache` | object | - | Cache configuration block. |
| `mode` | string | `"off"` | Cache operational mode: `"off"`, `"shadow"`, or `"on"`. |
| `exact` | boolean | `true` | Enable SHA-256 exact matching on normalized prompt messages. |
| `semantic` | boolean | `false` | Enable vector similarity semantic caching in Redis. |
| `threshold` | float | `0.86` | Calibrated cosine similarity threshold (verified via Wilson 95% upper bound $\le 1\%$). |
| `ttl` | duration | `24h` | Cache entry expiration time. |
| `version` | integer | `1` | Cache generation version. Incrementing invalidates historical entries instantly. |
| `embedding_route` | string | `""` | Name of internal route serving embedding vectors for queries. |
| `per_user` | boolean | `false` | Partition cache keys by client user identifier (`user` field). |
| `max_entry_bytes` | integer | `262144` | Maximum payload size in bytes cached in Redis (default 256KB). |

---

## 8. Guardrails (`guard`)

Input and output safety filters configured under `routes[].guard`.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `guard` | object | - | Root guardrails configuration. |
| `injection` | object | - | Prompt injection and jailbreak detection stage. |
| `enabled` | boolean | `false` | Enable prompt injection or PII redaction filter. |
| `threshold` | float | `0.70` | Heuristic classifier score threshold for triggering action. |
| `action` | string | `"block"` | Action taken on injection detection: `"block"` (400) or `"warn"`. |
| `pii` | object | - | Personally Identifiable Information filter. |
| `mode` | string | `"redact"` | Redaction mode: `"redact"` (reversible placeholders) or `"mask"` (`[REDACTED]`). |

---

## 9. Smart Routing & Auto-Rollback (`smart_route`)

Statistical cost reduction routing under `routes[].smart_route`.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `smart_route` | object | - | Smart routing configuration. |
| `mode` | string | `"off"` | Smart route mode: `"off"`, `"shadow"`, or `"on"`. |
| `cheap_target` | string | - | Fast/economical model target (e.g. `mock-a/mock-small`). |
| `strong_target` | string | - | Capable baseline model target (e.g. `mock-b/mock-large`). |
| `max_tokens_for_cheap` | integer | `1500` | Token limit ceiling for queries eligible for the cheap model. |
| `knn_enabled` | boolean | `false` | Use K-Nearest Neighbors semantic classifier. |
| `knn_route` | string | `""` | Route used to embed queries for KNN classification. |
| `knn_k` | integer | `5` | Number of nearest neighbors evaluated in KNN lookup. |
| `confidence_threshold` | float | `0.75` | Minimum neighbor agreement required to route to cheap model. |

---

## 10. Latency Hedging (`hedge`)

Proactive racing of slow requests configured under `routes[].hedge`.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `hedge` | object | - | Hedged request configuration. |
| `enabled` | boolean | `false` | Enable latency hedging for route. |
| `delay` | duration | `0s` | Fixed delay before launching secondary request (`0s` = dynamic EWMA TTFT + 2·dev). |
| `max_extra` | float | `0.10` | Maximum fraction of total route traffic permitted to be hedged (budget cap). |

---

## 11. System Defaults (`defaults`)

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `defaults` | object | - | Global default parameters. |
| `max_tokens_reserve` | integer | `4096` | Upper limit on completion tokens reserved during rate limit admission checks. |
| `default_max_tokens` | integer | `2048` | Fallback max tokens when client omits `max_tokens` in request. |

---

## 12. Service Level Objectives (`slos`)

Defines latency and error budgets per target model for health degradation tracking.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `slos` | map | `{}` | Keyed by `provider/model` string. |
| `ttft_ms` | float | `800.0` | Maximum acceptable Time to First Token before registering a breach. |
| `min_tps` | float | `10.0` | Minimum acceptable token throughput per second. |
| `max_error_rate` | float | `0.05` | Maximum allowable 5xx error rate fraction. |

---

## 13. EWMA Health Tracking (`health`)

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `health` | object | - | Health monitor tuning parameters. |
| `alpha` | float | `0.3` | EWMA decay factor ($0 < \alpha \le 1$). Higher values weight recent requests heavier. |
| `breaches` | integer | `3` | Consecutive breaches required to mark a target degraded. |
| `recover` | duration | `30s` | Continuous clean window required to restore target from degraded to healthy. |
| `min_samples` | integer | `10` | Minimum request samples before evaluating degradation. |
| `probe_interval` | duration | `5s` | Interval between synthetic background probes for degraded targets. |

---

## 14. Model Context Protocol Proxy (`mcp_servers` & `mcp_insecure_hosts`)

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `mcp_servers` | list | `[]` | List of upstream MCP servers reverse-proxied under `/mcp/{name}`. |
| `name` | string | - | Unique MCP server name matching URL slug. |
| `url` | string | - | Upstream MCP server URL (e.g. `https://mcp.internal:8443`). |
| `headers_env` | map | `{}` | Map of HTTP header names to environment variable names holding credentials. |
| `mcp_insecure_hosts` | list | `[]` | List of hostnames allowed to connect over unencrypted HTTP (default requires HTTPS). |

---

## Complete Annotated Example

```yaml
server:
  addr: ":8080"
  admin_addr: "0.0.0.0:9090"

secrets:
  kek: local
  local_kek_file: /run/secrets/local_kek
  cache_ttl: 60s

providers:
  - name: mock-a
    type: openai
    base_url: "http://mockllm-a:8081/v1"
    api_key_db: true
  - name: mock-b
    type: openai
    base_url: "http://mockllm-b:8081/v1"
    api_key_db: true

pricing:
  mock-a/mock-small: {input: 0.10, output: 0.40, cached_input: 0.05}
  mock-b/mock-large: {input: 1.00, output: 4.00, cached_input: 0.50}

routes:
  - name: default
    strategy: fallback
    timeout: 30s
    stream_idle_timeout: 10s
    targets:
      - provider: mock-a
        model: mock-small
      - provider: mock-b
        model: mock-large
    retry:
      max_attempts: 3
      base_delay: 100ms
    hedge:
      enabled: true
      delay: 0s
      max_extra: 0.10
    cache:
      mode: on
      exact: true
      semantic: false
      ttl: 24h
      version: 1
      per_user: false
      max_entry_bytes: 262144
    guard:
      injection:
        enabled: true
        threshold: 0.70
        action: block
      pii:
        enabled: true
        mode: redact
    smart_route:
      mode: on
      cheap_target: mock-a/mock-small
      strong_target: mock-b/mock-large
      max_tokens_for_cheap: 1500
      knn_enabled: false
      knn_route: ""
      knn_k: 5
      confidence_threshold: 0.75

defaults:
  max_tokens_reserve: 4096
  default_max_tokens: 2048

slos:
  mock-a/mock-small:
    ttft_ms: 800
    min_tps: 10
    max_error_rate: 0.05
  mock-b/mock-large:
    ttft_ms: 800
    min_tps: 10
    max_error_rate: 0.05

health:
  alpha: 0.3
  breaches: 3
  recover: 30s
  min_samples: 10
  probe_interval: 5s

mcp_servers:
  - name: github-mcp
    url: "https://mcp.github.internal"
    headers_env:
      Authorization: GITHUB_TOKEN

mcp_insecure_hosts:
  - "localhost"
  - "127.0.0.1"
```
