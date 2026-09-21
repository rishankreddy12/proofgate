# Show HN: ProofGate – an LLM gateway that measures what its cache and routing cost you

**GitHub:** https://github.com/proofgate/proofgate  
**Benchmarks:** [docs/benchmarks.md](../docs/benchmarks.md)  
**Threat Model:** [docs/threat-model.md](../docs/threat-model.md)  

Hi HN,

We built **ProofGate**, an open-source LLM gateway (Go / Apache-2.0) designed around a simple premise: semantic caching and cheap-model routing are only useful if you can mathematically prove they aren't serving wrong answers or degrading response quality.

ProofGate runs semantic caching in shadow mode first, calibrating cosine similarity thresholds against human-reviewed evaluations using exact Wilson score confidence intervals (95% upper bound on false hits $\le 1\%$). For model routing, it measures quality deltas continuously using position-debiased pairwise LLM judging with 2,000-iteration paired bootstrap confidence intervals, automatically rolling back to baseline models within seconds if quality drops.

Under heavy streaming load (1,000 open-loop RPS on 4 vCPUs), ProofGate adds just **{{bench:overhead/stream-1000-gateway-1.json:ttft.p99_ms}} ms** of p99 TTFT overhead while delivering **{{bench:phase3/routing-report-before.json:percent_saved}}%** verified cost reduction and sustaining **{{bench:overhead/saturation.json:saturation_rps}} RPS** before saturation. Every secret is envelope-encrypted with AES-256-GCM in PostgreSQL, the container is non-root distroless with zero shell binaries or provider SDKs, and every release is keylessly signed with Cosign, Syft SBOMs, and SLSA provenance.

---

### What ProofGate Does NOT Do (Yet)

To be completely honest about its current scope:
1. **No OpenAI Responses API (Realtime / WebRTC):** Focuses on standard `/v1/chat/completions` and `/v1/embeddings`.
2. **No AWS Bedrock SigV4 Authentication:** Supports OpenAI, Anthropic, Google Gemini, Ollama, vLLM, and generic OpenAI-compatible endpoints.
3. **No Multi-Candidate Completions (`n > 1`):** Optimized for high-throughput single-stream (`n=1`) production inference.
4. **Per-Replica Health State:** Health degradation EWMA trackers run locally per pod; they do not gossip state across replicas over the network.
5. **No Cloud Telemetry or Tracking:** ProofGate sends zero telemetry to the authors. You own your metrics in your own Prometheus and ClickHouse.

We would love your feedback and thoughts on our statistical verification approach!
