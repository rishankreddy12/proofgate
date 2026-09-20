# ProofGate

An open-source Go LLM gateway that proves its optimisations are safe.

One OpenAI-compatible API in front of OpenAI, Anthropic, Gemini and local models (Ollama, vLLM), with
distributed token-aware rate limits, budgets, fallbacks, circuit breakers and full tracing.

> Status: v0.1.0, core gateway. Safe semantic caching, quality-verified routing, SLO-aware failover,
> agent-run budgets and published benchmarks are on the roadmap.

## Quickstart (no API keys needed)

```bash
make up                                   # Postgres, Redis, two mock providers, three gateway replicas
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
| Rate limits | Requests and tokens per minute, one atomic Redis Lua script, shared by all replicas, pre-charge then reconcile |
| Budgets | Monthly USD budget per tenant, integer micro-USD accounting |
| Keys | SHA-256 hashed, route allow-lists, shown once |
| Observability | Prometheus metrics (including gateway overhead), OpenTelemetry GenAI spans, access logs without bodies or keys |

## Known limitations

- Concurrent requests can overshoot a budget by the cost of calls already in flight.
- A revoked key keeps working for up to 30 s on each replica (auth cache).
- `n > 1` and the Responses API are not supported yet.
