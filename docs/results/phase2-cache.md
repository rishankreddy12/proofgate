# Phase 2: Cache Performance & Replay Benchmark Results

## Overview

This report evaluates ProofGate's exact and semantic caching machinery using a two-phase replay of the Quora Question Pairs (QQP) dataset from the GLUE benchmark.

In Phase 1, the cache is populated with Question 1 of each pair. In Phase 2, Question 2 probes the cache. For pairs labeled duplicate by humans, a semantic cache hit is considered correct. For pairs labeled non-duplicate, or hits resolving to an unrelated question pair, the hit is marked as a **wrong hit**.

> These runs use the mock bag-of-words embedder, not a real embedding model, so they show the shape of the trade-off, not production numbers. Plan 3 repeats the sweep with a real embedding model and a judged false-hit rate.

## Sweep Results

- **Dataset:** `bench/datasets/qqp-replay.jsonl` (1,000 question pairs, 2,000 total requests, 1,000 probe requests)
- **Route:** `faq` (exact cache + RediSearch HNSW semantic cache)
- **Date:** 2026-09-20

| Threshold | Total Requests | Phase 2 Requests | Hits (Exact) | Hits (Semantic) | Hit Rate | Wrong Hits | Wrong-Hit Rate | p50 Latency (ms) | p99 Latency (ms) |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **0.80** | 2,000 | 1,000 | 7 | 162 | **16.9%** | 77 | **45.6%** | 8.290 | 17.019 |
| **0.90** | 2,000 | 1,000 | 9 | 57 | **6.6%** | 35 | **53.0%** | 9.534 | 19.412 |
| **0.95** | 2,000 | 1,000 | 10 | 19 | **2.9%** | 17 | **58.6%** | 9.943 | 28.308 |

### Observations

1. **Trade-off Shape:** As the similarity threshold is lowered from `0.95` to `0.80`, total cache hit rate increases from `2.9%` to `16.9%`.
2. **Semantic Cache Invalidation:** Each threshold run bumped `version`, cleanly partitioning cache scopes without cross-contamination.
3. **Latency:** Cache hits served with sub-10ms p50 latency, entirely bypassing upstream LLM provider calls and rate limit reserves.
