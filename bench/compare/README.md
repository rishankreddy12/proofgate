# Gateway Comparison Benchmark

This directory contains the reproducible comparison harness benchmarking **ProofGate**, **LiteLLM**, and **Bifrost** against the same deterministic mock upstream.

## Fairness Rules

1. **Identical Upstream:** Same host, same `mockllm` instance, same scenarios and ordering, three repeats, median reported.
2. **Identical Resource Limits:** Every gateway container is strictly constrained to **4 CPUs** and **8 GB RAM** (`deploy.resources.limits: {cpus: "4", memory: 8g}`).
3. **No Auxiliary Work:** Semantic caching, guardrails, external logging, and spending telemetry are turned **off** across all gateways so only proxy latency and overhead are measured.
4. **Canonical Production Configuration:** Each gateway uses its vendor-recommended configuration file checked into this directory.
5. **Exact Environments Recorded:** Pinned container image digests, commit SHAs, host OS, CPU model, and core counts are recorded in every result JSON file.
6. **Transparent Scope:** The results page states what this benchmark does *not* show:
   - Does not simulate wide-area internet jitter or real LLM provider queueing.
   - Does not benchmark multi-region distributed clusters.
   - Does not measure feature parity (e.g. ProofGate's mathematical proof layer, safe semantic caching, and automatic rollback).
   Each project's own published numbers are linked directly.
7. **No Hidden Failures:** If a gateway cannot run a scenario or crashes under load, the error is recorded explicitly in the result JSON rather than dropped.
