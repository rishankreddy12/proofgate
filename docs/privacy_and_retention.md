# ProofGate Data Retention, Privacy & Erasure Policy

This document outlines ProofGate's telemetry retention policy, privacy mechanisms, text anonymization modes, and tenant data erasure commands.

---

## 1. What Data is Stored and Retention Periods

ProofGate stores operational and evaluation telemetry in ClickHouse across the following tables:

| Table | Content Stored | Plaintext Content? | Text Column TTL | Row TTL |
| :--- | :--- | :--- | :--- | :--- |
| `usage_events` | Request IDs, tenant IDs, timestamps, tokens (prompt, completion, cached), latencies (TTFT, total), cost & savings micros. | ❌ No text stored | N/A | 180 Days |
| `cache_shadow` | Semantic similarity scores, threshold, query, candidate query/answers. | Governed by `store_text` (`full`, `hash`, or `none`) | 72 Hours (`text_ttl_hours`) | 30 Days (`ttl_days`) |
| `routing_shadow` | Routing decision, scores, prompt, cheap & strong model answers, judge model. | Governed by `store_text` (`full`, `hash`, or `none`) | 72 Hours (`text_ttl_hours`) | 30 Days (`ttl_days`) |
| `routing_decisions` | Route name, decision, router score, actual cost, counterfactual cost. | ❌ No text stored | N/A | 180 Days |
| `mcp_calls` | Server, method name, tool name, run ID, governance decision, HTTP status, latency. | ❌ No text stored | N/A | 180 Days |

---

## 2. Text Anonymization & Prompt Privacy (`store_text`)

To protect customer privacy, ProofGate supports three modes for handling prompts and completions in shadow evaluation tables via the `analytics.store_text` configuration option (or `PROOFGATE_ANALYTICS_STORE_TEXT` env var):

1. **`hash` (Recommended / Default in Production):**
   - All prompt and completion strings are replaced with a cryptographic SHA-256 digest (`sha256:<hex>`).
   - Enables exact-match deduplication and audit tracking without exposing plaintext data to database operators or analytics systems.
2. **`none`:**
   - Prompt and completion strings are completely dropped (`""`). Only numeric scores, latencies, and metadata are retained.
3. **`full`:**
   - Raw prompt and completion strings are stored temporarily for human labeling and offline evaluation.

---

## 3. ClickHouse Column TTLs

In addition to whole-row TTLs, ProofGate implements granular ClickHouse **column-level TTLs** on all text fields in `cache_shadow` (`query`, `candidate_query`, `candidate_answer`, `actual_answer`) and `routing_shadow` (`prompt`, `cheap_answer`, `strong_answer`):

- **Default Column TTL:** 72 Hours (`analytics.text_ttl_hours: 72`).
- **Mechanism:** After 72 hours, ClickHouse automatically truncates the text column data to empty strings during background merges while leaving metadata and scores intact for metric tracking until the row TTL (`analytics.ttl_days: 30`) expires.

---

## 4. Pipeline Redaction (Guard Stage)

When the `guard` stage is enabled on a route (via `routes[].guard.pii.mode: "mask"` or `"block"`), PII and sensitive credentials are automatically identified and redacted *before* any request or response is processed, cached, or recorded in shadow storage. Shadow evaluation records therefore only ever contain sanitized representations of user requests.

---

## 5. Tenant Data Erasure (Right to be Forgotten)

In compliance with GDPR and data privacy regulations, ProofGate provides an administrative command to completely erase all telemetry records associated with a tenant:

```bash
proofgatectl tenant purge-data <tenant_id_or_name>
```

This command connects to ClickHouse and issues asynchronous partition mutations:
```sql
ALTER TABLE usage_events DELETE WHERE tenant_id = '<id>';
ALTER TABLE cache_shadow DELETE WHERE tenant_id = '<id>';
ALTER TABLE routing_shadow DELETE WHERE tenant_id = '<id>';
ALTER TABLE routing_decisions DELETE WHERE tenant_id = '<id>';
ALTER TABLE mcp_calls DELETE WHERE tenant_id = '<id>';
```

This ensures complete erasure of all operational, shadow, and audit telemetry for the tenant across the cluster.
