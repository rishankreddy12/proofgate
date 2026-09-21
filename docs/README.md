# ProofGate Documentation Index

Welcome to the comprehensive documentation for **ProofGate** — the enterprise high-performance LLM gateway with statistical verification, zero-trust key management, and zero vendor lock-in.

---

## Getting Started & Architecture

- **[10-Minute Quickstart](quickstart.md)**: From a clean clone to a working, OpenAI-compatible completion request in minutes, with zero external API keys needed.
- **[Configuration Reference](configuration.md)**: Complete, machine-verified reference for `proofgate.yaml`, covering routes, caching, SLOS, providers, and security.
- **[Portability & No-Lock-In Guide](portability.md)**: Details on open standards (OpenAI API, OpenTelemetry), customer data ownership, and migration guides.
- **[Autonomous Agent Governance & MCP Proxy](agents.md)**: Loop detection, per-run token and cost budgets, and secure Model Context Protocol reverse proxying.
- **[STRIDE Threat Model & Security Controls](threat-model.md)**: Comprehensive machine-checked security architecture with analysis of the March 2026 LiteLLM PyPI compromise.

---

## Benchmarks & Evaluation Results

- **[Reproducible Benchmarks & Gateway Comparison](benchmarks.md)**: Open-loop latency, TTFT streaming overhead, max throughput per vCPU, and fair comparisons against LiteLLM and Bifrost under identical hardware limits.
- **[Phase 2 Caching & Analytics Evaluation](results/phase2-cache.md)**: GLUE QQP evaluation replay, exact cache hit latency, and ClickHouse analytics.
- **[Phase 3 Proof Layer & Statistical Verification](results/phase3-proof.md)**: Wilson confidence bounds, paired bootstrap CI on routing quality, and Cohen's $\kappa$ judge agreement.
- **[Phase 4 Resilience & Failover Results](results/phase4.md)**: EWMA SLO routing, dynamic latency hedging, and automated runaway agent loop prevention.

---

## Deployment & Production Operations

- **[Production Helm Chart](../deploy/helm/proofgate/README.md)**: Hardened Kubernetes deployment with Pod Security Standards, NetworkPolicy, and Vault Transit integration.
- **[Docker Compose Quickstart](../deploy/docker-compose.yml)**: Local full-stack environment with PostgreSQL, Redis, ClickHouse, Prometheus, and Grafana.
- **[Security Policy & Vulnerability Reporting](../SECURITY.md)**: Vulnerability disclosure guidelines, supply-chain verification, and Cosign release signatures.
