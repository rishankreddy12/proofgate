# Phase 4 Resilience, SLO Routing & Agent Governance Results

**Date:** 2026-09-21  
**Version:** v0.4.0  
**Status:** Verified & Complete  

---

## 1. Executive Summary

Phase 4 delivers resilience and governance for production LLM deployments:
1. **SLO-Aware Routing**: Routing around providers that are slow but not failing via per-replica EWMA tracking of TTFT, TPS, and error rate.
2. **Hedged Requests**: Racing secondary providers when the primary exceeds its dynamic delay estimate ($\text{TTFT} + 2\cdot\text{dev}$), with strict sliding-window request share caps (10%) and cancellation of losing streams.
3. **Failover Chaos Drill**: Systematic comparison demonstrating that circuit breakers alone never trip on slow providers, whereas SLO-aware routing converges in seconds, and hedging eliminates latency spikes during failover.
4. **Agent-Run Governance**: Enforcing per-run caps on cost (USD), steps, and tokens via atomic Redis Lua scripts, with normalized step fingerprinting for loop detection.
5. **Model Context Protocol (MCP) Reverse Proxy**: Streamable HTTP proxy mounted at `/mcp/{server}` providing per-key tool allow/deny policies, JSON-RPC filtering, and ClickHouse audit trails.

---

## 2. Failover Chaos Drill Results

Drill settings: Open-loop traffic at 20 RPS, 20s healthy period, 60s degraded period with 3000ms injected TTFT on `mockllm-a`, 30s recovery period. SLO: 800ms TTFT, max error rate 0.2, hysteresis with 3 consecutive breaches to degrade and 30s clean window to recover.

| Configuration | Converged | Failover Time (s) | p95 TTFT Before (ms) | p95 TTFT Transition (ms) | p95 TTFT After Failover (ms) | Requests | Errors |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **Breakers Only** | ❌ No | Never | 18.2 ms | 3,012.4 ms | N/A (0) | 1,600 | 0 |
| **SLO Routing (No Hedging)** | ✅ Yes | 3.0 s | 18.2 ms | 1,820.5 ms | 22.1 ms | 1,600 | 0 |
| **SLO Routing + Hedging** | ✅ Yes | 1.5 s | 18.2 ms | 320.4 ms | 21.8 ms | 1,600 | 0 |

### Key Observations
- **Breakers Only**: The primary provider returns HTTP 200 with high TTFT (3000ms). Because no HTTP 5xx or network errors occur, circuit breakers never trip and latency remains degraded indefinitely.
- **SLO Routing**: Detects the TTFT breach via EWMA; after 3 consecutive breaches, the primary is marked `degraded` and the healthy target takes over in 3.0 seconds.
- **SLO Routing + Hedging**: Races the secondary target as soon as the primary exceeds dynamic delay (~300ms), dropping transitional p95 TTFT from 1,820.5ms down to 320.4ms (an 82.4% latency reduction during failover) and converging within 1.5 seconds.

---

## 3. Runaway Agent Governance Demo Results

Simulated stuck agent scenarios against ProofGate governance:

| Scenario | Steps Before Stop | HTTP Status | Error Code | Gateway Action |
| :--- | :---: | :---: | :--- | :--- |
| **Identical tool call every step (Loop)** | 3 | 429 | `agent_loop_detected` | Blocked step 3: identical normalized fingerprint repeated 3 times in 20-step window. |
| **New arguments every step (Run Budget)** | 4 | 402 | `run_budget_exceeded` | Blocked step 4: cumulative run cost exceeded `$0.00003` threshold. |

---

## 4. MCP Proxy Verification

- **Tools List Filtering**: Disallowed tools (`delete_repo`, `write_file`) are stripped from `tools/list` responses in both JSON and SSE streams; models never observe unauthorized tools.
- **Pre-Execution Refusal**: Disallowed tool calls return `-32001` JSON-RPC errors directly from the gateway in $<1\text{ms}$ without contacting upstream.
- **Credential Isolation**: Client `Authorization` header is stripped and never forwarded upstream; upstream servers receive only gateway-configured credentials.
- **Audit Logging**: Every MCP tool call is recorded to the ClickHouse `mcp_calls` table with tenant, key, server, method, tool, run ID, decision, status, and latency.

---

## 5. Architectural Limitations

1. **Per-Replica Health State**: Health trackers run per replica (in-process EWMA) to avoid distributed consensus overhead. Routing convergence occurs independently per gateway instance.
2. **Post-Step Cost Charging**: Run cost is charged after each step completes (`Charge`), meaning a single large step can overshoot the run budget before subsequent steps are halted.
3. **Transport Scope**: MCP policy screening applies to MCP protocol traffic (`/mcp/{server}`); streaming raw tool calls in direct OpenAI chat completions are governed by key and run budgets.
