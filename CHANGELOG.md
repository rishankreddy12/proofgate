# Changelog

All notable changes to ProofGate are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [v1.0.0] - 2026-09-21

### Added
- **Reproducible Benchmarking Suite**: Open-loop TTFT and latency generator (`cmd/loadgen`) and automated markdown report generator (`cmd/benchreport`) with CI marker validation (`-check`).
- **Same-Harness Competitor Comparison**: Standardized Docker Compose comparison harness benchmarking ProofGate, LiteLLM, and Bifrost under identical 4-CPU/8GB limits.
- **Production Hardened Helm Chart**: Kubernetes chart strictly conforming to Pod Security Standards (non-root `65532`, read-only root FS, dropped capabilities, dual health probes, NetworkPolicy, and Vault Transit integration).
- **Automated 10-Minute Quickstart**: Single-command startup (`make quickstart`) with zero external API keys required, provisioned Grafana dashboards, and automated CI runtime verification (`scripts/first_run_test.sh`).
- **Data Portability & Export (D7)**: Zero lock-in exports (`proofgatectl export usage` and `proofgatectl export config`) and comprehensive portability migration documentation.
- **Configuration Parity Testing**: Machine-checked reflection test (`internal/configdocs`) ensuring zero drift between `config.Config` struct tags and `docs/configuration.md`.
- **Launch Package**: Launch posts, Show HN draft, and Go/No-Go readiness checklist.

---

## [v0.5.0] - 2026-09-21

### Added
- **Zero-Trust Key Handling**: AES-256-GCM envelope encryption with per-record Data Encryption Keys (DEKs) wrapped by local KEK or HashiCorp Vault Transit engine.
- **PostgreSQL Credentials Store**: Stdin-only CLI (`proofgatectl provider-key set`) and advisory locks for zero-leakage secret updates.
- **Bounded In-Memory Cache**: 60-second TTL cache for decrypted provider keys with instant administrative cluster purge endpoint (`POST /admin/secrets/purge`).
- **Log Scrubber & Safe Panic Recovery**: High-performance slog handler redacting provider keys and regex patterns (`[REDACTED]`, `[REDACTED:SECRET]`) and panic recovery preventing body leakage.
- **Distroless Hardening**: Pinned distroless container image without shell binaries (`/bin/sh`) or interpreters.
- **Supply-Chain Verification**: SHA-pinned GitHub Actions, Cosign keyless signatures, Syft SPDX SBOMs, and SLSA provenance attestations.
- **STRIDE Threat Model**: Machine-checked STRIDE threat model backed by `internal/securitydocs` tests including the March 2026 LiteLLM PyPI compromise case study.

---

## [v0.4.0] - 2026-09-21

### Added
- **EWMA Target Health & SLO Routing**: Latency and error rate tracking with hysteresis (3 breaches to degrade, 30s clean window to recover) and synthetic background probes.
- **Latency Hedging**: Generic hedged execution racing secondary targets when primary attempt exceeds dynamic delay ($\text{EWMA TTFT} + 2\cdot\text{dev}$), capped by a 10% route traffic budget.
- **Agent Run Governance**: Redis Lua token budgets, cost limits, and normalized 32-character SHA-256 step fingerprint loop detection.
- **Model Context Protocol (MCP) Reverse Proxy**: Streaming SSE/JSON-RPC 2.0 proxy mounted at `/mcp/{server}` with deny-by-default tool policy filtering and ClickHouse audit logging.

---

## [v0.3.0] - 2026-09-21

### Added
- **Mathematical Proof Layer**: Exact Wilson score confidence intervals, 2,000-iteration paired percentile bootstrap intervals, and Cohen's Kappa inter-rater agreement.
- **Safe Semantic Cache Calibration**: Automated false-hit rate curve sweep over shadow data, selecting calibrated threshold where Wilson 95% upper bound on false hits $\le 1\%$.
- **Reversible Privacy Guardrails**: RFC-compliant PII detection (credit cards with Luhn, Aadhaar with Verhoeff, SSN, IP, Email) with placeholder substitution and streaming-safe token reconstruction.
- **Heuristic Injection Defense**: Prompt injection and jailbreak defense without external API latency.
- **Smart Model Routing & Auto-Rollback**: Fast-path rule classification and KNN semantic routing delivering verified cost savings with continuous bootstrap quality verification and automatic rollback.

---

## [v0.2.0] - 2026-09-21

### Added
- **Exact Prompt Caching**: Normalized prompt hashing and sub-millisecond in-memory and Redis response caching.
- **ClickHouse Analytics**: High-throughput asynchronous batch ingestion for usage events.
- **Grafana Dashboards**: Pre-provisioned dashboards for latency percentiles, token throughput, and cost analytics.

---

## [v0.1.0] - 2026-09-21

### Added
- **Core LLM Gateway**: High-throughput OpenAI-compatible HTTP reverse proxy in pure Go with zero third-party provider SDKs.
- **Token Bucket Rate Limiting**: Per-tenant requests-per-minute (RPM) and tokens-per-minute (TPM) enforcement via Redis.
- **Resilient Fallback**: Automatic retries and fallback across multi-provider target lists.
- **Deterministic Mock Provider (`mockllm`)**: Built-in mock provider for local development, reproducible testing, and benchmarks.
