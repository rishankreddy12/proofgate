# ProofGate

**ProofGate is a production-oriented, OpenAI-compatible LLM gateway for multi-tenant routing, semantic caching, provider resilience, agent governance, privacy controls, and evidence-based automatic rollback.**

[![CI](https://github.com/proofgate/proofgate/actions/workflows/ci.yml/badge.svg)](https://github.com/proofgate/proofgate/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](go.mod)

ProofGate sits between applications and one or more LLM providers. Clients use a
stable OpenAI-compatible API while ProofGate applies authentication, tenant
policy, rate limits, budgets, routing, retries, caching, guardrails, telemetry,
and provider-specific protocol translation.

The project is implemented as a Go 1.26 static binary and uses standard HTTP
adapters rather than vendor-specific AI SDKs. PostgreSQL stores control-plane
state, Redis provides fast coordination and ephemeral state, and ClickHouse
stores bounded operational and evaluation telemetry.

> **Source-distribution note:** this GitHub snapshot intentionally excludes
> test-only source files, test fixtures, end-to-end harnesses, and test script
> folders. The production build, deployment manifests, configuration examples,
> operational scripts, and documentation remain included.

---

## Contents

- [Why ProofGate](#why-proofgate)
- [Capabilities](#capabilities)
- [Architecture](#architecture)
- [Quickstart](#quickstart)
- [Using the API](#using-the-api)
- [Providers and routes](#providers-and-routes)
- [Secure provider credentials](#secure-provider-credentials)
- [Administration and control plane](#administration-and-control-plane)
- [Tenant erasure](#tenant-erasure)
- [Observability and health](#observability-and-health)
- [Configuration](#configuration)
- [Deployment](#deployment)
- [Project layout](#project-layout)
- [Operational checklist](#operational-checklist)
- [Development and verification](#development-and-verification)
- [Limitations](#limitations)
- [Documentation](#documentation)
- [License](#license)

---

## Why ProofGate

LLM gateways make provider access easier, but they can also introduce hidden
quality, privacy, and reliability risks. ProofGate is designed around the
following operating principles:

1. **Open client contract** — applications use familiar OpenAI-compatible
   endpoints and standard HTTP authentication.
2. **Explicit tenant isolation** — API keys resolve to tenants, policies,
   routes, budgets, cache namespaces, and agent-run limits.
3. **Evidence before optimization** — semantic caching and cheap-model routing
   can run in shadow mode, record evidence, and roll back automatically when
   quality bounds become unsafe.
4. **Fail safely** — deadlines, bounded response bodies, retries, circuit
   breakers, readiness checks, and clear non-2xx errors prevent ambiguous
   provider behavior from becoming successful-looking client responses.
5. **Keep secrets out of the control plane** — provider credentials are
   envelope-encrypted, cached briefly in memory, and invalidated across
   replicas using digest- or identifier-only Redis events.

---

## Capabilities

### API gateway

- `POST /v1/chat/completions` with unary and streaming responses.
- `POST /v1/embeddings` for embedding-capable routes.
- `GET /v1/models` for OpenAI-compatible model discovery.
- OpenAI-compatible provider adapters plus Anthropic and Gemini protocol
  adapters.
- Per-route retries, deadlines, first-token timeouts, stream-idle timeouts,
  circuit breakers, and bounded upstream response reads.
- HTTP status preservation and structured API errors for provider failures,
  rate limits, timeouts, budget failures, and unavailable dependencies.

### Routing and resilience

- Ordered fallback routing.
- Cheapest-target routing using configured pricing.
- EWMA health tracking for latency, throughput, and error rates.
- Optional Redis or signed gossip health sharing.
- Dynamic latency hedging with a configurable extra-traffic budget.
- Atomic configuration reloads without restarting the gateway.

### Proof layer

- Exact and semantic response caching.
- Shadow-mode cache evaluation before enabling semantic reuse.
- Embedding-backed similarity decisions with configurable thresholds.
- Smart routing with cheap/strong model comparisons.
- Position-debiased pairwise evaluation and confidence-bound monitoring.
- Automatic rollback to safer routing or cache modes when quality evidence
  crosses configured limits.

### Security and privacy

- SHA-256 API-key lookup with bounded positive and negative authentication
  caches.
- Per-IP failed-auth throttling.
- AES-256-GCM envelope encryption for database-backed provider credentials.
- Local KEK or HashiCorp Vault Transit integration.
- Cluster-wide key and credential invalidation over Redis pub/sub without
  publishing plaintext secrets.
- PII and prompt-injection guard stages with reversible streaming restoration.
- Configurable analytics text modes: `full`, `hash`, or `none`.
- Distroless, non-root, read-only container hardening.

### Agent and MCP governance

- Redis Lua-backed per-run step, token, and cost budgets.
- Normalized request fingerprinting for repeated-step loop detection.
- Run-ID enforcement for agent requests.
- MCP reverse proxy under `/mcp/{server}`.
- Deny-by-default MCP tool policy filtering.
- Bounded MCP request bodies and upstream timeouts.
- Auditable MCP call records in ClickHouse.

### Operations

- Authenticated admin control plane with `admin`, `operator`, and `viewer`
  roles.
- Admin sessions with idle and absolute timeouts, revocation, and lockout
  protection.
- Provider probes, configuration inspection, cache purge, credential rotation,
  key revocation, tenant erasure, and audit events.
- Prometheus metrics and Grafana dashboards in the Compose environment.
- Liveness and dependency-aware readiness endpoints.

---

## Architecture

```text
                         +----------------------+
 OpenAI SDK / HTTP ----> |  ProofGate gateway    | ----> OpenAI
                         |                      | ----> Anthropic
                         | auth + tenant policy | ----> Gemini
                         | routing + resilience| ----> Ollama/vLLM
                         | cache + guardrails  |
                         | proof + telemetry   |
                         +----------+-----------+
                                    |
                 +------------------+------------------+
                 |                  |                  |
             PostgreSQL           Redis            ClickHouse
          control-plane state  cache/control bus   usage + proof data
```

### Runtime responsibilities

| Component | Responsibility | Durable? |
| --- | --- | :---: |
| Gateway binary | API handling, auth middleware, routing, providers, guardrails | No |
| PostgreSQL | Tenants, API keys, admin users, credentials, overrides, audit records | Yes |
| Redis | Rate limits, budgets, cache entries, agent runs, sessions, control bus | Mostly ephemeral |
| ClickHouse | Usage events, routing evidence, cache evidence, MCP audit data | Yes, TTL-managed |
| Prometheus | Metrics scraping and time-series monitoring | Deployment-dependent |
| Grafana | Local dashboards over Prometheus and ClickHouse | Deployment-dependent |

### Request flow

1. The request reaches the public listener.
2. The auth middleware extracts a bearer token or `api-key` header and hashes
   it before PostgreSQL lookup.
3. Tenant policy and API-key route permissions are attached to the request
   context.
4. The selected route applies guardrails, rate limits, budget reservation,
   cache lookup, and routing policy.
5. The provider adapter executes the request with bounded deadlines and
   normalized response/error handling.
6. Usage and proof events are queued for ClickHouse without blocking the main
   response path.
7. Reserved tokens and budget amounts are reconciled with actual usage.

---

## Quickstart

### Prerequisites

- Git.
- Go 1.26 for local builds and CLI commands.
- Docker Desktop with the **Linux container engine** enabled. On Windows, the
  host OS is Windows but the Compose images run inside Docker Desktop's WSL2
  Linux engine.
- At least 8 GB of available memory is recommended for PostgreSQL, Redis,
  ClickHouse, three gateways, mock providers, Prometheus, and Grafana.

### Start the local stack

```bash
git clone https://github.com/proofgate/proofgate.git
cd proofgate
make quickstart
```

`make quickstart` generates the local KEK when needed, starts the Compose
stack, waits for dependencies, creates a demo tenant, and writes a development
API key to `.quickstart-key`. That file is ignored by Git and must not be
shared.

The helper prints a working request. The equivalent manual request is:

```bash
KEY="$(cat .quickstart-key)"

curl http://127.0.0.1:18080/v1/chat/completions \
  -H "Authorization: Bearer ${KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Hello from ProofGate"}]
  }'
```

### Compose endpoints

| Service | Host endpoint | Purpose |
| --- | --- | --- |
| Gateway 1 data plane | `http://127.0.0.1:18080` | OpenAI-compatible API |
| Gateway 1 admin | `http://127.0.0.1:19090` | Authenticated control plane |
| Gateway 2 data/admin | `18085` / `19091` | Additional gateway replica |
| Gateway 3 data/admin | `18084` / `19092` | Additional gateway replica |
| PostgreSQL | `127.0.0.1:15432` | Control-plane database |
| Redis | `127.0.0.1:16379` | Cache, coordination, control bus |
| ClickHouse HTTP | `127.0.0.1:18123` | Analytics HTTP endpoint |
| ClickHouse native | `127.0.0.1:19000` | Analytics native endpoint |
| Grafana | `http://127.0.0.1:13000` | Local dashboards |

Stop the stack with:

```bash
docker compose -f deploy/docker-compose.yml down
```

Use `down -v` only when you intentionally want to remove the local database,
Redis, and ClickHouse volumes.

### Windows Docker troubleshooting

If Docker Desktop is open but `docker ps` reports that the daemon cannot be
reached, restart the WSL2 backend and start Docker Desktop again:

```powershell
wsl --shutdown
docker desktop start
docker context use desktop-linux
docker ps
```

The expected result is a successful response from the `desktop-linux` context.
The project requires Linux containers; Windows containers are not compatible
with the PostgreSQL, Redis, ClickHouse, and gateway images in the Compose file.

---

## Using the API

### Chat completions

ProofGate accepts the standard OpenAI chat-completions shape. The `model` value
is normally a configured ProofGate route name, such as `default` or `faq`.

```bash
curl http://127.0.0.1:18080/v1/chat/completions \
  -H "Authorization: Bearer ${KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "default",
    "stream": false,
    "messages": [
      {"role": "system", "content": "Answer concisely."},
      {"role": "user", "content": "What is a circuit breaker?"}
    ],
    "max_tokens": 200
  }'
```

For streaming:

```bash
curl -N http://127.0.0.1:18080/v1/chat/completions \
  -H "Authorization: Bearer ${KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "default",
    "stream": true,
    "messages": [{"role": "user", "content": "Stream a short answer."}]
  }'
```

### Embeddings

Embedding requests use an `embeddings: true` route and a provider/model that
supports embeddings:

```bash
curl http://127.0.0.1:18080/v1/embeddings \
  -H "Authorization: Bearer ${KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "embed-real",
    "input": ["A sentence to vectorize"]
  }'
```

Embedding routes can also serve semantic-cache lookups. A cache-enabled route
uses `cache.embedding_route` to name the embedding route that generates query
vectors.

### Models

```bash
curl http://127.0.0.1:18080/v1/models \
  -H "Authorization: Bearer ${KEY}"
```

### Python OpenAI client

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:18080/v1",
    api_key=open(".quickstart-key", encoding="utf-8").read().strip(),
)

response = client.chat.completions.create(
    model="default",
    messages=[{"role": "user", "content": "Hello ProofGate"}],
)
print(response.choices[0].message.content)
```

### MCP proxy

When an MCP server is configured, clients connect through:

```text
http://127.0.0.1:18080/mcp/<server-name>
```

The same ProofGate bearer token and, for governed agent traffic, the
`X-ProofGate-Run-Id` header are applied. Tool access is deny-by-default and is
filtered according to the API-key MCP policy.

---

## Providers and routes

### Provider types

The current configuration supports:

| Type | Protocol | Typical base URL |
| --- | --- | --- |
| `openai` | OpenAI-compatible JSON and SSE | `https://api.openai.com/v1` |
| `anthropic` | Anthropic Messages API | `https://api.anthropic.com` |
| `gemini` | Gemini Generative Language API | `https://generativelanguage.googleapis.com` |

OpenAI-compatible providers such as Ollama and vLLM can use the `openai`
adapter. Provider adapters are implemented with `net/http`; no provider SDK is
required in the gateway binary.

### Minimal provider and route configuration

```yaml
providers:
  - name: openai
    type: openai
    base_url: "https://api.openai.com/v1"
    api_key_env: OPENAI_API_KEY

pricing:
  openai/gpt-4o-mini: {input: 0.15, output: 0.60}

routes:
  - name: production
    strategy: fallback
    targets:
      - {provider: openai, model: gpt-4o-mini}
```

Provider target names are written as `provider/model`. Pricing values are USD
per one million tokens. Pricing is required when budget enforcement or
cheapest-target routing needs a cost calculation.

### Fallback routing

```yaml
routes:
  - name: default
    strategy: fallback
    retry:
      max_attempts: 2
      base_delay: 200ms
    timeout: 90s
    deadline: 120s
    targets:
      - {provider: openai, model: gpt-4o-mini}
      - {provider: anthropic, model: claude-3-5-sonnet}
```

### Cheapest routing

```yaml
routes:
  - name: economical
    strategy: cheapest
    targets:
      - {provider: openai, model: gpt-4o-mini}
      - {provider: gemini, model: gemini-1.5-flash}
```

Every target in a `cheapest` route must have a matching pricing entry.

### Smart routing and proof mode

Smart routing can choose between a cheap and strong target, initially in shadow
mode so decisions can be evaluated before affecting user-visible traffic:

```yaml
routes:
  - name: support
    targets:
      - {provider: openai, model: gpt-4o-mini}
      - {provider: openai, model: gpt-4o}
    smart_route:
      mode: shadow
      cheap_target: openai/gpt-4o-mini
      strong_target: openai/gpt-4o
```

The proof monitor records comparisons and can automatically return a route to a
safer mode when the configured quality confidence interval is unacceptable.

---

## Secure provider credentials

ProofGate supports two explicit credential sources. Do not configure both on a
single provider.

### Environment-backed credentials

Use environment variables for simple deployments:

```yaml
providers:
  - name: openai
    type: openai
    base_url: "https://api.openai.com/v1"
    api_key_env: OPENAI_API_KEY
```

Register the variable in the deployment environment, never in Git:

```bash
export OPENAI_API_KEY="..."
```

### Database-backed credentials

For credential rotation and cluster-wide invalidation, use the encrypted
database source:

```yaml
secrets:
  kek: local
  local_kek_file: /run/secrets/local_kek

providers:
  - name: openai
    type: openai
    base_url: "https://api.openai.com/v1"
    api_key_db: true
```

After bootstrapping an admin user and logging in:

```bash
printf '%s\n' "$OPENAI_API_KEY" | \
  proofgatectl --profile local provider credential set --provider openai
```

The gateway seals the key with AES-256-GCM envelope encryption and stores only
encrypted material in PostgreSQL. Decrypted values are cached briefly in
memory. Rotation purges local caches and publishes a provider identifier and
credential version over Redis; plaintext credentials never enter the control
bus.

The configured KEK must be available to every gateway replica. For production,
prefer Vault Transit or another managed key-encryption boundary over a local
file shared broadly across hosts.

---

## Administration and control plane

### Bootstrap an administrator

The first admin is created directly against PostgreSQL:

```bash
export DATABASE_URL="postgres://proofgate:proofgate@127.0.0.1:15432/proofgate?sslmode=disable"
proofgatectl bootstrap-admin --username admin
```

### Configure the CLI profile and log in

```bash
proofgatectl profile add --name local --server http://127.0.0.1:19090
proofgatectl profile use --name local
proofgatectl login
proofgatectl whoami
```

The default standalone admin address is `127.0.0.1:9090`. Compose publishes
gateway 1's authenticated admin listener as `127.0.0.1:19090`.

### Common commands

```bash
proofgatectl status
proofgatectl health
proofgatectl provider list
proofgatectl provider test --name openai
proofgatectl provider credential list
proofgatectl config show --output json
proofgatectl config reload
proofgatectl cache purge --tenant <tenant-id>
proofgatectl key revoke --id <key-id>
proofgatectl tenant purge-data <tenant-id-or-name>
```

Remote administration uses authenticated control-plane endpoints. Direct
database commands are intended for initial bootstrap and controlled operational
procedures. Use `--output json` for automation.

### RBAC summary

| Role | Intended access |
| --- | --- |
| `admin` | Full user, session, provider, configuration, cache, key, and erasure administration |
| `operator` | Provider operations, configuration reload, cache operations, health, and status |
| `viewer` | Read-only configuration, health, status, provider metadata, and audit visibility |

All mutations should be performed through authenticated control-plane routes or
the CLI profile backed by a control-plane session.

---

## Tenant erasure

Tenant erasure is a cross-store operation. It is idempotent and returns a
component receipt instead of claiming success when a dependency is unavailable.

```bash
proofgatectl tenant purge-data <tenant-id-or-name>
```

The operation covers:

- PostgreSQL API keys, tenant-scoped provider credential overrides, and the
  tenant record.
- Redis exact and semantic cache entries, cache indexes, agent-run state,
  fuzzy-loop embeddings, budget counters, and tenant-tagged session extensions.
- ClickHouse usage events, routing decisions, cache shadow data, routing shadow
  data, MCP calls, and cache labels associated with tenant-owned shadows.
- Positive authentication caches on every connected gateway replica through a
  tenant invalidation event.

ClickHouse `ALTER TABLE ... DELETE` mutations are asynchronous. The receipt
therefore reports ClickHouse as submitted/pending until background mutation
processing completes. A partial or unavailable component produces a non-success
CLI result and should be investigated before the erasure is treated as complete.

See [privacy and retention](docs/privacy_and_retention.md) for retention,
prompt anonymization, and data ownership details.

---

## Observability and health

### Health endpoints

| Endpoint | Meaning |
| --- | --- |
| `GET /healthz` | Process liveness only; does not require PostgreSQL, Redis, or ClickHouse |
| `GET /readyz` | Bounded runtime and dependency readiness check |
| `GET /metrics` | Prometheus metrics on the metrics listener |
| `GET /admin/cp/health` | Authenticated target-health snapshot |
| `GET /admin/cp/status` | Authenticated gateway status and configuration counts |

Liveness intentionally remains process-only so a temporary database or Redis
outage does not cause an orchestrator restart loop. Readiness checks the active
runtime and required dependencies with bounded contexts and returns `503` when
the gateway should not receive traffic.

### Metrics and dashboards

The Compose stack includes Prometheus and Grafana. The bundled Grafana
dashboard is available at:

```text
http://127.0.0.1:13000/d/proofgate
```

Useful metric families include request status, provider latency, target breaker
state, cache activity, budget behavior, proof queue drops, and credential-cache
events.

### Analytics retention

ClickHouse tables have row TTLs and configurable text-column TTLs. Set:

```yaml
analytics:
  store_text: hash       # full, hash, or none
  text_ttl_hours: 72
  ttl_days: 30
```

`hash` is the recommended production mode when exact-match evidence is useful
without retaining raw prompts and completions.

---

## Configuration

The main configuration file is YAML. The reference deployment configuration is
[`deploy/proofgate.yaml`](deploy/proofgate.yaml). Configuration values can also
be overridden with the documented `PROOFGATE_*` environment variables.

### Important top-level sections

| Section | Purpose |
| --- | --- |
| `server` | Public, admin, metrics, timeout, body-size, and drain settings |
| `database` | PostgreSQL URL and pool sizing |
| `redis` | Redis URL for cache, coordination, and control events |
| `analytics` | ClickHouse DSN, batchers, retention, and text policy |
| `admin_auth` | Control-plane sessions, lockouts, and authentication |
| `auth` | API-key cache TTLs and cache capacity |
| `secrets` | Local KEK or Vault-backed credential encryption |
| `providers` | Provider protocol, base URL, headers, and credential source |
| `pricing` | USD per million input/output/cached tokens |
| `routes` | Targets, routing strategy, retry, cache, guard, and smart routing |
| `budget` | Reservation mode and pricing requirements |
| `health` | Probing, SLO tracking, and health-sharing settings |
| `mcp` | MCP body limits and upstream timeout policy |
| `proof` | Evaluation thresholds, consent, and monitoring settings |

Validate a configuration before deploying it:

```bash
go run ./cmd/proofgatectl config validate --file deploy/proofgate.yaml
```

### Safe listener defaults

The binary defaults to a loopback admin listener:

```yaml
server:
  addr: ":8080"
  admin_addr: "127.0.0.1:9090"
  metrics_addr: "127.0.0.1:9091"
```

If an admin listener is bound to a non-loopback address, `admin_auth.enabled`
must be true. The Compose configuration intentionally overrides the container
admin bind to `0.0.0.0:9090` while publishing only loopback host ports.

---

## Deployment

### Docker Compose

Compose provides the complete local stack:

```bash
mkdir -p deploy/secrets
go run ./cmd/proofgatectl kek generate > deploy/secrets/local_kek.b64
docker compose -f deploy/docker-compose.yml up -d --build --wait
```

Before production-like use, replace mock routes with real provider routes,
configure a durable KEK strategy, set database credentials through secrets, and
place the admin listener behind an authenticated internal network or TLS-aware
ingress.

### Kubernetes and Helm

The Helm chart is under [`deploy/helm/proofgate`](deploy/helm/proofgate). It
defaults to three replicas, non-root UID `65532`, a read-only root filesystem,
all Linux capabilities dropped, a `RuntimeDefault` seccomp profile, a Pod
Disruption Budget, and an optional NetworkPolicy.

Example installation:

```bash
helm upgrade --install proofgate deploy/helm/proofgate \
  --namespace proofgate \
  --create-namespace \
  --set image.repository=ghcr.io/example/proofgate \
  --set image.tag=<release-tag>
```

Use an immutable image digest in production. Supply PostgreSQL, Redis,
ClickHouse, and KEK/Vault settings through Kubernetes Secrets or an external
secret manager. Do not place provider keys in Helm values committed to Git.

### Container hardening

The production image is built for a minimal runtime and runs as UID `65532`.
Compose and Helm apply read-only filesystem, dropped capabilities, and
no-new-privileges controls. The gateway should not need a shell, package
manager, or writable application directory at runtime.

---

## Project layout

```text
cmd/
  proofgate/          Gateway executable and runtime wiring
  proofgatectl/       Bootstrap, admin, provider, tenant, and key CLI
  mockllm/            Deterministic local provider used by Compose
  benchreport/        Benchmark result validation/reporting
internal/
  api/                OpenAI-compatible request and error types
  app/                Pipeline assembly
  auth/               API-key authentication and principal context
  cache/              Exact/semantic cache stages and invalidation
  control/            Redis control-bus protocol
  provider/           HTTP provider adapters and error normalization
  server/             Public API and admin control-plane handlers
  secrets/            Envelope encryption, KEK handling, and key cache
  store/              PostgreSQL persistence and tenant erasure
  analytics/          ClickHouse migrations, batchers, and purgers
  proof/              Shadow evidence, evaluation, and rollback logic
  mcpproxy/           MCP reverse proxy and policy enforcement
  smartroute/         Smart routing and confidence tracking
deploy/
  docker-compose.yml  Full local stack
  proofgate.yaml      Reference configuration
  helm/proofgate/     Kubernetes chart
docs/                 Detailed operations, security, privacy, and benchmark docs
scripts/              Deployment and supply-chain checks
```

Test-only source and harness directories are intentionally excluded from the
current GitHub source distribution.

---

## Operational checklist

Before exposing ProofGate to production traffic:

- [ ] Use PostgreSQL, Redis, and ClickHouse instances with backups and access
      controls.
- [ ] Use HTTPS for external provider URLs and for any non-loopback admin path.
- [ ] Enable `admin_auth` and bootstrap a unique administrator password.
- [ ] Use Vault Transit or an equivalently protected KEK boundary.
- [ ] Configure `api_key_db: true` for credentials that need rotation and
      cluster invalidation.
- [ ] Configure provider pricing when budgets or cheapest routing are enabled.
- [ ] Start semantic caching in `shadow` mode and review evidence before
      enabling reuse.
- [ ] Set `analytics.store_text` to `hash` or `none` unless raw text retention
      is explicitly approved.
- [ ] Configure bounded request, provider, stream-idle, and drain timeouts.
- [ ] Scrape `/metrics` and alert on readiness failures, provider errors,
      breaker state, queue drops, and budget/credential failures.
- [ ] Test tenant erasure receipts and document who approves completion.
- [ ] Pin container images and deployment action references.
- [ ] Keep `.env`, KEKs, provider keys, session files, and `.quickstart-key`
      outside version control.

---

## Development and verification

Build the complete Go source tree:

```bash
go build ./...
```

Run static analysis:

```bash
go vet ./...
```

Validate deployment configuration:

```bash
docker compose -f deploy/docker-compose.yml config --quiet
```

Run the supply-chain and image checks used by this repository:

```bash
bash scripts/check_dockerfile_go_version.sh
bash scripts/check_no_provider_sdk.sh go.mod
```

The project keeps production verification commands in the repository while
test-only files and folders are excluded from the GitHub source snapshot by
policy.

---

## Limitations

- The public API focuses on `/v1/chat/completions`, `/v1/embeddings`, and
  `/v1/models`; it does not implement the OpenAI Responses Realtime/WebRTC
  surface.
- Native provider support currently covers OpenAI-compatible, Anthropic, and
  Gemini protocols. AWS Bedrock SigV4 is not built in.
- The gateway is optimized for one completion per request; multi-candidate
  `n > 1` workloads are not the primary contract.
- Health tracking is primarily per replica. Redis and signed gossip sharing are
  available for deployments that need shared health signals.
- ClickHouse erasure mutations are asynchronous and must be verified through
  operational receipts and mutation monitoring.
- Provider API-key invalidation is immediate for connected replicas through
  Redis; an isolated replica falls back to its configured in-memory cache TTL.

---

## Documentation

- [Documentation index](docs/README.md)
- [Quickstart](docs/quickstart.md)
- [Configuration reference](docs/configuration.md)
- [Admin CLI and control plane](docs/admin-cli.md)
- [Privacy, retention, and tenant erasure](docs/privacy_and_retention.md)
- [Agent governance and MCP](docs/agents.md)
- [Portability and data export](docs/portability.md)
- [Threat model and security controls](docs/threat-model.md)
- [Benchmarks and methodology](docs/benchmarks.md)
- [Helm deployment reference](deploy/helm/proofgate/README.md)
- [Security policy](SECURITY.md)

---

## License

ProofGate is licensed under the [Apache License, Version 2.0](LICENSE).
