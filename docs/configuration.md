# ProofGate Configuration Reference

This document provides an exhaustive reference for `proofgate.yaml`. All fields are checked by automated regression tests to guarantee documentation parity with the active codebase.

---

## 1. Top-Level Structure

A canonical `proofgate.yaml` is composed of the following top-level blocks:
- `server`: HTTP listener addresses, timeouts, and body size limits for data plane and control plane.
- `database`: PostgreSQL connection string and connection pool tuning.
- `redis`: Redis connection string for distributed caching and state.
- `analytics`: ClickHouse connection and asynchronous telemetry batcher tuning.
- `telemetry`: OpenTelemetry trace collection and service naming.
- `breakers`: Circuit breaker failure thresholds and cooldown durations.
- `auth`: Client API key in-memory caching and bounds.
- `mcp`: Global Model Context Protocol proxy configuration.
- `secrets`: Zero-trust Key Encryption Key (KEK) and Vault configuration.
- `providers`: Upstream LLM backend definitions.
- `pricing`: Model cost rates per 1M tokens.
- `budget`: Monthly tenant budget reservation and enforcement settings.
- `proof`: Automated quality proof and semantic threshold floor settings.
- `routes`: Virtual routing topologies, fallback strategies, caching, and guardrails.
- `defaults`: Global system fallback limits.
- `slos`: Target Service Level Objectives for health and hedging.
- `health`: Exponentially Weighted Moving Average (EWMA) health tracker tuning.
- `mcp_servers`: Model Context Protocol upstream reverse proxy endpoints.
- `mcp_insecure_hosts`: Whitelisted hosts for unencrypted MCP communication.
- `watcher_interval`: Dynamic configuration file polling interval.
- `override_interval`: Dynamic database override polling interval.

---

## 2. Server Configuration (`server`)

Controls bind addresses for client traffic and management APIs.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `server` | object | - | Root configuration for server listeners. |
| `addr` | string | `":8080"` | Listen address for public OpenAI-compatible data plane APIs (overridable via `PROOFGATE_ADDR`). |
| `admin_addr` | string | `"127.0.0.1:9090"` | Listen address for private management, reload, control-plane APIs, and readiness checks (overridable via `PROOFGATE_ADMIN_ADDR`). **Never expose to the public internet.** |
| `metrics_addr` | string | `"127.0.0.1:9091"` | Listen address for isolated `/metrics` and `/healthz` endpoints (overridable via `PROOFGATE_METRICS_ADDR`). |
| `enable_pprof` | boolean | `false` | Enable Go runtime profiling endpoints under `/debug/pprof/` on the admin listener (requires `admin` role with `debug:pprof` permission). |
| `max_request_body_bytes` | integer | `10485760` | Maximum allowed request body size in bytes for chat and embeddings (default 10MB). |
| `max_upstream_response_bytes` | integer | `33554432` | Maximum allowed response body size in bytes from upstream providers (default 32MB). |
| `read_header_timeout` | duration | `10s` | Maximum time allowed to read HTTP request headers before closing connection. |
| `read_timeout` | duration | `15s` | Maximum time allowed to read the entire HTTP request including body. |
| `idle_timeout` | duration | `120s` | Maximum time to keep idle keep-alive HTTP connections open. |
| `drain_timeout` | duration | `30s` | Graceful shutdown drain timeout for in-flight requests. |
| `trusted_proxies` | list of strings | `[]` | List of trusted reverse proxy CIDRs or IP addresses for client IP extraction from `X-Forwarded-For`. |

```yaml
server:
  addr: ":8080"
  admin_addr: "127.0.0.1:9090"
  metrics_addr: "127.0.0.1:9091"
  enable_pprof: false
  max_request_body_bytes: 10485760
  max_upstream_response_bytes: 33554432
  read_header_timeout: 10s
  read_timeout: 15s
  idle_timeout: 120s
  drain_timeout: 30s
  trusted_proxies:
    - "127.0.0.1/32"
    - "10.0.0.0/8"
```

---

## 2.1 Database Configuration (`database`)

Configures persistent state and metadata storage in PostgreSQL.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `database` | object | - | PostgreSQL persistence configuration. |
| `url` | string | `""` | PostgreSQL connection URL (e.g. `postgres://user:pass@host:5432/db`). Falls back to `DATABASE_URL` environment variable. |
| `max_conns` | integer | `20` | Maximum number of concurrent connections in the PostgreSQL connection pool. |

```yaml
database:
  url: postgres://proofgate:proofgate@postgres:5432/proofgate?sslmode=disable
  max_conns: 20
```

---

## 2.2 Redis Configuration (`redis`)

Configures distributed caching, token bucket rate limiting, and agent run state tracking.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `redis` | object | - | Redis backend configuration. |
| `url` | string | `""` | Redis connection URL (e.g. `redis://redis:6379/0`). Falls back to `REDIS_URL` environment variable. |

```yaml
redis:
  url: redis://redis:6379/0
```

---

## 2.3 Analytics & Batchers (`analytics`)

Configures ClickHouse telemetry ingestion and asynchronous event batching queues.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `analytics` | object | - | Analytics and ClickHouse event logging settings. |
| `clickhouse_dsn` | string | `""` | ClickHouse connection DSN. Falls back to `CLICKHOUSE_DSN` environment variable. |
| `usage_batcher` | object | - | Batcher queue configuration for standard usage events. |
| `mcp_batcher` | object | - | Batcher queue configuration for MCP audit events. |
| `capacity` | integer | `50000` / `20000` | Maximum in-memory ring buffer capacity before dropping events. |
| `batch_size` | integer | `5000` / `2000` | Number of events buffered before triggering a bulk insert. |
| `flush_interval` | duration | `1s` | Maximum time to wait before flushing pending records. |
| `flush_timeout` | duration | `10s` | Maximum time allowed to flush pending analytics during gateway shutdown. |
| `text_ttl_hours` | integer | `72` | Column TTL in hours for text fields (`query`, `prompt`, candidate/actual answers) in ClickHouse shadow tables. Overridable via `PROOFGATE_ANALYTICS_TEXT_TTL_HOURS`. |
| `ttl_days` | integer | `30` | Row TTL in days for shadow evaluation rows in ClickHouse. Overridable via `PROOFGATE_ANALYTICS_TTL_DAYS`. |
| `store_text` | string | `"hash"` | Privacy policy for prompt/answer text stored in analytics: `full` (store complete text), `hash` (SHA-256 digest `sha256:...`), or `none` (omit text entirely). Overridable via `PROOFGATE_ANALYTICS_STORE_TEXT`. |

```yaml
analytics:
  clickhouse_dsn: clickhouse://proofgate:proofgate@clickhouse:9000/proofgate
  usage_batcher:
    capacity: 50000
    batch_size: 5000
    flush_interval: 1s
  mcp_batcher:
    capacity: 20000
    batch_size: 2000
    flush_interval: 1s
  flush_timeout: 10s
```

---

## 2.4 Distributed Tracing (`telemetry`)

Configures OpenTelemetry distributed tracing and export.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `telemetry` | object | - | OpenTelemetry exporter configuration. |
| `otlp_endpoint` | string | `""` | OTLP/HTTP collector endpoint. Falls back to `OTEL_EXPORTER_OTLP_ENDPOINT` environment variable. |
| `service_name` | string | `"proofgate"` | Service name attribute attached to emitted spans. |

```yaml
telemetry:
  otlp_endpoint: http://otel-collector:4318
  service_name: proofgate
```

---

## 2.5 Circuit Breakers (`breakers`)

Configures automatic fault isolation when upstreams fail.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `breakers` | object | - | Global circuit breaker parameters. |
| `threshold` | integer | `5` | Consecutive upstream failures before tripping breaker open. |
| `cooldown` | duration | `30s` | Time window a tripped target remains open before admitting a canary probe. |

```yaml
breakers:
  threshold: 5
  cooldown: 30s
```

---

## 2.6 Authentication Cache (`auth`)

Configures in-memory key authentication validation and negative caching.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `auth` | object | - | Gateway authentication middleware tuning. |
| `cache_ttl` | duration | `30s` | In-memory cache duration for valid tenant API keys. |
| `negative_cache_ttl` | duration | `5s` | In-memory cache duration for rejected/invalid API keys to mitigate brute-force lookups. |
| `max_cached_keys` | integer | `100000` | Upper bound on cached keys to prevent memory exhaustion under attack. |

```yaml
auth:
  cache_ttl: 30s
  negative_cache_ttl: 5s
  max_cached_keys: 100000
```

---

## 2.7 Global MCP Proxy (`mcp`)

Global options for the Model Context Protocol reverse proxy.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `mcp` | object | - | Global MCP proxy options. |
| `max_request_body_bytes` | integer | `4194304` | Maximum allowed request body size in bytes for MCP JSON-RPC payloads (default 4MB). |
| `request_timeout` | duration | `60s` | Maximum HTTP request timeout for outbound POST calls to upstream MCP servers (default 60s). |

```yaml
mcp:
  max_request_body_bytes: 4194304
  request_timeout: 60s
```

---

## 2.8 Admin Control-Plane Authentication (`admin_auth`)

Controls authentication, session management, and rate limiting for the administrative control-plane API.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `admin_auth` | object | - | Control-plane admin authentication settings. |
| `enabled` | boolean | `false` | When `true`, enables session-based RBAC authentication for control-plane endpoints. |
| `max_login_attempts` | integer | `5` | Maximum consecutive failed login attempts before temporary account lockout. |
| `lockout_duration` | duration | `15m` | Lockout duration after exceeding maximum login attempts. |
| `session_idle_timeout` | duration | `30m` | Session inactivity timeout before requiring re-authentication. |
| `session_absolute_timeout` | duration | `12h` | Maximum total lifetime of an administrative session. |
| `chat` | object | - | Resource limits and routing controls for administrative interactive chat playground (`POST /admin/cp/chat`). |
| `rpm` | integer | `60` | Maximum requests per minute admitted for administrative chat sessions. |
| `tpm` | integer | `100000` | Maximum tokens per minute admitted for administrative chat sessions. |
| `budget_usd` | float | `10.0` | Monthly spending ceiling in USD for administrative chat testing. |
| `allowed_routes` | list of strings | `[]` | Specific routes permitted for administrative playground calls (empty allows all). |

```yaml
admin_auth:
  enabled: false
  max_login_attempts: 5
  lockout_duration: 15m
  session_idle_timeout: 30m
  session_absolute_timeout: 12h
  chat:
    rpm: 60
    tpm: 100000
    budget_usd: 10.0
    allowed_routes:
      - "default"
```

---

## 2.9 Budget Enforcement (`budget`)

Controls atomic budget reservation and pricing requirements for tenant spend limits.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `budget` | object | - | Monthly tenant budget enforcement configuration. |
| `reserve` | string | `"strict"` | Reservation mode (`"strict"` for atomic pre-allocation before upstream calls, `"off"` for legacy post-call accounting). |
| `require_pricing` | boolean | `true` | If true, budgeted tenants will fail-closed (reject with 500) if any target in the route plan lacks configured pricing. |

```yaml
budget:
  reserve: strict
  require_pricing: true
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
| `vault_token_file` | string | `"/var/run/secrets/kubernetes.io/serviceaccount/token"` | Path to Kubernetes service account token file for Vault authentication. |
| `cache_ttl` | duration | `60s` | Maximum TTL for decrypted in-memory provider credentials before automatic eviction. |
| `previous_keks` | list of objects | `[]` | List of previous KEK configurations preserved to decrypt historical credentials during KEK rotation. |

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
| `allow_insecure_base_url` | boolean | `false` | When `true`, permits plain HTTP base URLs for non-loopback hosts. |

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
| `timeout` | duration | `120s` | Overall per-attempt execution timeout. |
| `deadline` | duration | `90s` | Maximum end-to-end plan deadline across all targets and retries (default 90s). |
| `first_token_timeout` | duration | `15s` | Maximum time allowed to receive the first token in streaming requests before failing over (default 15s). |
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
| `acknowledge_uncalibrated` | boolean | `false` | When `true`, allows `cache.mode: on` without verified calibration overrides. |
| `allow_client_key` | boolean | `false` | When `true`, permits client to supply query text via `X-ProofGate-Cache-Query` header with context isolation. |
| `audit_sample_rate` | float | `0.0` | Probability (0.0 to 1.0) of sampling semantic hits for background upstream audit evaluation and false-hit detection. |

> **Context Isolation:** When `allow_client_key` is enabled, ProofGate stores a SHA-256 hash of all preceding context messages (`ContextHash`) and enforces context equality on semantic hits, preventing cross-tenant or cross-document data leakage in RAG workloads. Multi-user applications sharing an API key should also enable `per_user: true` so that user contexts and scopes are isolated.

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
| `agent_run_ttl` | duration | `1h` | Default time-to-live for tracking autonomous agent run state in Redis. |
| `agent_loop_repeats` | integer | `3` | Default repeat count triggering loop prevention when policy omits it. |
| `agent_loop_window` | integer | `20` | Default step window for evaluating loop detection history. |
| `agent_fuzzy_threshold` | float | `0.95` | Default semantic cosine similarity threshold for fuzzy loop detection. |
| `embedder_cache_size` | integer | `10000` | In-memory LRU cache capacity for semantic cache embedding vectors. |

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
| `probe_timeout` | duration | `10s` | Maximum execution timeout for synthetic background health probes. |
| `gossip_addr` | string | `""` | Local bind address for cluster health gossip memberlist (e.g. `0.0.0.0:7946`). |
| `gossip_peers` | list | `[]` | Seed list of peer addresses for health state synchronization across the cluster. |
| `share_mode` | string | `"redis"` | Cross-replica health state sharing mode: `redis` (via Redis hashes), `gossip` (UDP memberlist), or `none`. Overridable via `PROOFGATE_HEALTH_SHARE_MODE`. Defaults to `redis` when Redis is configured. |
| `gossip_secret` | string | `""` | HMAC-SHA256 secret key for signing and verifying UDP gossip datagrams (overridable via `PROOFGATE_GOSSIP_SECRET`). |

---

## 13.1 Dynamic Polling Intervals

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `watcher_interval` | duration | `5s` | Interval between polling `proofgate.yaml` for configuration reload. |
| `override_interval` | duration | `10s` | Interval between polling PostgreSQL database for runtime routing overrides. |

---

## 13.2 Quality Proof & Calibration (`proof`)

Configuration for automated quality proofs and semantic threshold calibration floors.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `proof` | object | - | Proof system and calibration configuration. |
| `min_threshold` | float | `0.86` | Documented minimum calibrated floor for semantic cache similarity thresholds. |

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

## 15. Model Capabilities (`capabilities`)

Defines token limits, multimodal capabilities, and tool support constraints per model.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `capabilities` | map | `{}` | Map of capability constraints keyed by model identifier. |
| `max_context_tokens` | integer | `0` | Upper limit on total context window token count. |
| `supports_vision` | boolean | `false` | Whether model accepts multimodal vision/image inputs. |
| `supports_tools` | boolean | `nil` | Whether model supports tool/function calling (`nil` falls back to provider default). |

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

---

## 16. Proof Quality & Automated Rollback (`proof`)

Configures automated statistical quality monitoring, leader-gated rollbacks, and judge privacy.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `proof` | object | - | Root proof and quality monitoring configuration. |
| `min_threshold` | float | `0.86` | Documented minimum calibrated semantic cache threshold. |
| `monitor_interval` | duration | `60s` | Polling interval for automated quality and rollback evaluations. |
| `rollback_threshold` | float | `-0.05` | CI quality delta floor triggering automatic rollback of smart routing. |
| `min_pairs` | integer | `50` | Minimum shadow sample pairs required before admitting statistical rollback decisions. |
| `shadow_consent` | boolean | `false` | When `false`, prompts do not leave the tenant's chosen provider path for background judging without explicit opt-in. |
| `allowed_judge_providers` | list of strings | `[]` | Allow-list of upstream providers permitted to act as quality judges. |

```yaml
proof:
  min_threshold: 0.86
  monitor_interval: 60s
  rollback_threshold: -0.05
  min_pairs: 50
  shadow_consent: false
  allowed_judge_providers:
    - openai
```

