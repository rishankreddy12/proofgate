# ProofGate

An open-source Go LLM gateway that proves its optimisations are safe.

One OpenAI-compatible API in front of OpenAI, Anthropic, Gemini and local models (Ollama, vLLM), with
distributed token-aware rate limits, budgets, fallbacks, circuit breakers and full tracing.

> Status: v0.3.0, proof layer: safe semantic cache calibration, quality-verified smart routing with auto-rollback, and reversible PII guardrails.
> SLO-aware failover, agent-run budgets and published benchmarks are on the roadmap.

## Quickstart (no API keys needed)

```bash
make up                                   # Postgres, Redis, ClickHouse, Prometheus, Grafana, mock providers, three gateway replicas
export DATABASE_URL=postgres://proofgate:proofgate@localhost:5432/proofgate?sslmode=disable
go run ./cmd/proofgatectl tenant create --name demo --rpm 600
KEY=$(go run ./cmd/proofgatectl key create --tenant demo --name me)
curl http://localhost:8080/v1/chat/completions -H "Authorization: Bearer $KEY" \
  -d '{"model":"default","messages":[{"role":"user","content":"hello"}]}'
```

Point any OpenAI SDK at `http://localhost:8080/v1` with that key.

## What it does today

| Feature | How |
|---|---|
| OpenAI-compatible API | `/v1/chat/completions` (streaming and tools), `/v1/embeddings`, `/v1/models` |
| Providers | OpenAI-compatible (OpenAI, Azure OpenAI v1, Ollama, vLLM), Anthropic, Gemini. `net/http` only, no SDKs |
| Streaming | Unbuffered SSE; failover before the first token; idle timeout; client disconnect cancels upstream; cost in an HTTP trailer |
| Failover | Retries with full-jitter backoff; 429 and auth errors move to the next target; bad requests are never retried; per-target circuit breakers |
| Exact cache | Tenant-isolated, scoped by route, system prompt and sampling parameters; tags and purge |
| Semantic cache | RediSearch HNSW per tenant and scope; calibrated safely via shadow mode, false-hit sweep, and LLM-judge verification |
| Safe cache calibration | Shadow logging to ClickHouse; pointwise LLM Judge with position-bias swap; false-hit curve with Wilson 95% confidence intervals; human agreement gate ($\kappa \ge 0.70$) |
| PII Guardrails | High-precision regex + checksums (RFC 5322 email, IPv4/IPv6, US Phone, US SSN, Luhn credit cards, Verhoeff $D_5$ Aadhaar); reversible token masking and stream unmasking |
| Prompt injection defense | Heuristic scoring (instruction overrides, jailbreaks, roleplay persona shifts, base64 obfuscation) with configurable block/flag actions |
| Quality-verified smart routing | Fast-path rule classification + kNN embedding distance-weighted voting for cost-effective model selection (e.g. gpt-4o-mini vs gpt-4o) |
| Quality degradation auto-rollback | Pairwise shadow evaluations in ClickHouse; bootstrap confidence interval monitoring on quality deltas ($\Delta \text{quality} = Q_{\text{routed}} - Q_{\text{strong}}$); auto-rollback via PostgreSQL `route_overrides` if upper 95% CI bound $< -0.05$ |
| Analytics & audit | Every request, token usage, guardrail trigger, and proof evaluation logged to ClickHouse; Grafana dashboard with cost by tenant and net cache savings |
| Rate limits & budgets | Token & request rate limits in Redis Lua scripts; monthly USD budget per tenant in micro-USD |
| Observability | Prometheus metrics (gateway overhead, p99 latencies, cache hit rates, guardrail blocks), OpenTelemetry GenAI spans |

## Proof Layer Architecture

ProofGate provides mathematical and statistical safety guarantees for production AI systems:

1. **Safe Semantic Cache Calibration**: Prevents semantic hallucinations by evaluating candidate similarity thresholds offline and in shadow mode. Thresholds are calibrated with Wilson score 95% confidence intervals to enforce an empirical false-hit rate $\le 1.0\%$.
2. **Double-Blind Judge Verification**: Internal LLM judges evaluate pairs with positional order swapping (`A vs B` and `B vs A`) to cancel position bias and judge consistency scoring.
3. **Human Agreement Gate**: CLI tool (`proofgatectl label cache`) enables domain experts to label candidate pairs. Automated calibration enforces Cohen's Kappa $\kappa \ge 0.70$ inter-rater reliability before activating threshold updates.
4. **Reversible PII Stream Redaction**: Sensitive data (emails, credit cards, SSNs, Aadhaar numbers, IP addresses) are masked into typed placeholders (e.g., `[EMAIL_1]`) before reaching upstream LLMs, and reversibly restored in streaming token chunks.
5. **Quality-Verified Smart Routing & Rollback**: Directs simple queries to fast, cost-effective models while routing complex prompts to frontier models. A continuous ClickHouse monitor samples production pairs, calculates non-parametric bootstrap confidence intervals, and automatically triggers Postgres route overrides if quality degrades.

## Proof Admin Endpoints

Admin endpoints are exposed on port `9090`:

- `GET /admin/proof/cache/curve?route=<name>&sample_size=500` - Returns false-hit curve with empirical error rates and Wilson 95% confidence bounds.
- `POST /admin/proof/cache/apply` - Applies an empirically calibrated threshold to a route's semantic cache configuration.
- `GET /admin/proof/routing/report?route=<name>&window=7d` - Returns cost savings, routing distribution, and quality delta bootstrap confidence intervals.
- `POST /admin/cache/purge` - Purges exact and semantic caches by tenant or tag.

## CLI Commands

```bash
# Manage tenants and API keys
go run ./cmd/proofgatectl tenant create --name production --rpm 1200
go run ./cmd/proofgatectl key create --tenant production --name prod-app

# Review semantic cache candidate hits for human-in-the-loop agreement
go run ./cmd/proofgatectl label cache --route default --count 20

# Run offline calibration sweeps
go run ./cmd/cacheeval -dataset bench/datasets/qqp-replay.jsonl -n 500 -output bench/results/cache_curve.json
go run ./cmd/routeeval -output bench/results/routing_eval.json
```

## Cache invalidation

ProofGate provides three mechanisms to invalidate cached responses:

| Need | Use |
|---|---|
| Content behind some answers changed (for example a docs page) | tag requests with `X-ProofGate-Cache-Tags: docs-v2`, then purge by tag |
| One tenant's cache is wrong | purge by tenant (and route) |
| A route's model or prompt changed for everyone | bump `cache.version` in config; old entries become unreachable and expire by TTL |

### Invalidation via Admin API

Purge cached entries by issuing `POST /admin/cache/purge` on the admin port:

```bash
# Purge all entries with tag "docs" for tenant "acme"
curl -X POST http://localhost:9090/admin/cache/purge \
  -H "Content-Type: application/json" \
  -d '{"tenant_id": "acme", "tags": ["docs"]}'

# Purge entire cache for tenant "acme"
curl -X POST http://localhost:9090/admin/cache/purge \
  -H "Content-Type: application/json" \
  -d '{"tenant_id": "acme"}'
```

## Known limitations

- Concurrent requests can overshoot a budget by the cost of calls already in flight.
- A revoked key keeps working for up to 30 s on each replica (auth cache).
- `n > 1` and the Responses API are not supported yet.
