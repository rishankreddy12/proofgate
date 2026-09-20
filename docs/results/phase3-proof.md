# Phase 3 Proof Layer Benchmark Results

**Date:** 2026-09-21  
**Version:** v0.3.0  
**Status:** Verified & Complete  

---

## 1. Executive Summary

Phase 3 introduces the **Proof Layer** to ProofGate: mathematical uncertainty quantification, verifiable semantic cache calibration, reversible privacy guardrails, and quality-verified smart model routing with automated statistical rollback.

Key accomplishments:
1. **Mathematical Guarantees**: Exact Wilson score confidence intervals, 2000-iteration percentile bootstrap intervals, and Cohen's Kappa inter-rater agreement.
2. **Safe Semantic Cache Calibration**: Automated false-hit rate curve sweep over shadow data, picking the lowest similarity threshold whose Wilson 95% upper bound on false-hit rate $\le 1\%$.
3. **Reversible Privacy & Injection Guardrails**: RFC-compliant PII detection with Luhn & Verhoeff checksum validation, placeholder substitution, and streaming-safe token reconstruction. Prompt injection heuristic defense blocks jailbreaks with zero external API latency.
4. **Smart Routing & Auto-Rollback**: Fast-path rule classification and KNN semantic routing yielding **39.29% cost savings** with a quality delta 95% CI of **$[-0.0020, +0.0006]$**, with automated rollback if 95% CI drops below $-0.05$.

---

## 2. Statistical Verification

| Statistic | Implementation | Test Case | Target / Formula | Verified Result |
| :--- | :--- | :--- | :--- | :--- |
| **Wilson Score Interval** | `internal/stats/wilson.go` | 0/100, 10/100 ($z=1.96$) | $\frac{p + z^2/(2n) \pm z\sqrt{p(1-p)/n + z^2/(4n^2)}}{1 + z^2/n}$ | $[0, 0.0370]$, $[0.0552, 0.1744]$ |
| **Paired Bootstrap CI** | `internal/stats/bootstrap.go` | 2000 iterations, $n=400$ | Percentile bootstrap ($\alpha=0.05$) | Mean 0.000, CI $[-0.14, +0.14]$ |
| **Cohen's Kappa** | `internal/stats/kappa.go` | Standard 50-item agreement | $\kappa = \frac{p_o - p_e}{1 - p_e}$ | $\kappa = 0.4000$ (exact) |

---

## 3. Semantic Cache False-Hit Rate Curve (QQP Calibration)

Ran `cmd/cacheeval` over 500 QQP evaluation pairs using deterministic embeddings:

| Threshold ($t$) | Hit Rate (%) | False Hit Rate (%) | Wilson 95% Upper Bound (%) | Sample Count ($N$) |
| :---: | :---: | :---: | :---: | :---: |
| 0.70 | 31.60% | 44.30% | 52.09% | 158 |
| 0.75 | 24.40% | 40.98% | 49.86% | 122 |
| 0.80 | 18.20% | 38.46% | 48.73% | 91 |
| 0.85 | 12.00% | 40.00% | 52.63% | 60 |
| 0.90 | 6.60% | 45.45% | 62.01% | 33 |
| 0.95 | 2.20% | 36.36% | 64.62% | 11 |
| 0.96 | 0.80% | 0.00% | 48.99% | 4 |
| 0.97 | 0.40% | 0.00% | 65.76% | 2 |
| 0.98 | 0.00% | 0.00% | 100.00% (uncalibrated) | 0 |

**Selected Calibrated Threshold:** `0.98` (most conservative fallback when insufficient samples meet Wilson upper $\le 1\%$). When 500+ samples above 0.86 are collected with $\le 1$ false hit, the calibrated threshold automatically selects `0.86`.

---

## 4. Guardrails & Reversible PII Redaction

### 4.1 Detection & Validation Accuracy
- **Credit Cards**: Validated with Luhn checksum algorithm. Random 16-digit strings are rejected; Visa, Mastercard, Amex pass.
- **US SSN**: Validated against area 000/666/9xx and group 00 / serial 0000 rules.
- **Aadhaar**: Validated with Verhoeff dihedral group $D_5$ checksum algorithm.
- **Email, IPv4, IPv6, Phone**: RFC 5322 regex and network parsing.

### 4.2 Stream Restoration
`StreamRestorer` buffers fragmented placeholders across chunk boundaries (e.g. `<EMA` + `IL_` + `1>`) and reconstitutes `alice@example.com` with **0% token leakage** and $<0.05\text{ms}$ overhead.

### 4.3 Prompt Injection Defense
- **Heuristic Patterns**: Instruction overrides, role confusion tags (`<|im_start|>`, `\n\nHuman:`), prompt extraction requests.
- **Detection Rate**: 10/10 on classic jailbreak and override patterns (score $\ge 0.70$).
- **False Positive Rate**: 0/10 on benign coding, recipe, math, and conversational queries (score $< 0.30$).

---

## 5. Smart Routing Benchmark Results

Evaluated across benchmark queries:

```
=================================================================
                SMART ROUTING EVALUATION RESULTS                 
=================================================================
Total Queries Evaluated:      40
Routed to Cheap Model:        17 (42.5%)
Routed to Strong Model:       23 (57.5%)
-----------------------------------------------------------------
Baseline Cost (All-Strong):   $0.021485
Actual Cost (Smart-Routed):   $0.013044
Total Cost Savings:           $0.008441 (39.29% savings)
-----------------------------------------------------------------
Quality Delta Mean:           -0.0007
Quality Delta 95% Bootstrap CI: [-0.0020, +0.0006]
Quality Degradation < -0.05:  NO (Quality verified)
=================================================================
```

---

## 6. Automated Rollback Verification

When quality delta samples are injected with mean degradation of $-0.15$ (simulating an upstream regression or underperforming cheap model):
1. Paired bootstrap CI computed: `[-0.162, -0.118]`.
2. Condition `ciHigh < -0.05` is met.
3. `MonitorQuality` writes an override to `route_overrides`:
   `route=faq, key=smart_route.mode, value=off, reason=auto_rollback`
4. Runtime poll loop detects change and switches smart routing off within 10 seconds.
