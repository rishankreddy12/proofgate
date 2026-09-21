# Your Semantic Cache Is Serving Wrong Answers. Here Is How to Measure It.

Every LLM gateway today advertises "semantic caching" as a silver bullet to cut API bills in half. The concept sounds intuitive: embed incoming prompts, store them in a vector database, and return previous responses if cosine similarity exceeds some threshold (typically 0.85 or 0.90).

What vendor benchmarks rarely mention is the **false-hit rate**: the percentage of cache hits where the prompt had a superficially similar vector representation, but a completely different semantic meaning or user intent.

In customer-facing production systems, a false cache hit isn't just an efficiency loss — it is a silent, confident hallucination that serves User A's answer to User B.

---

## 1. The Threshold Trade-Off

When you increase the similarity threshold:
- You decrease the hit rate (saving fewer dollars).
- You decrease the probability of false hits.

When you decrease the threshold:
- You save more dollars.
- Your application begins serving nonsensical or dangerous answers.

Where should you set the threshold? Virtually every developer team guesses. In an audit of open-source setups, default thresholds range arbitrarily between 0.80 and 0.92 with zero empirical grounding on customer data.

---

## 2. Shadow Mode & Calibration

ProofGate solves this by introducing **Shadow Mode** and statistical calibration:
1. **Shadow Replay**: When semantic caching is in `shadow` mode, every request is sent upstream normally. In parallel, the vector search executes and logs candidate cache hits to ClickHouse without serving them.
2. **Calibrating the Judge**: An LLM judge evaluates whether the candidate query and target query are semantically interchangeable. Crucially, ProofGate checks the judge's agreement against human raters using **Cohen's Kappa** ($\kappa$). In our calibration over GLUE QQP, the judge achieved inter-rater agreement of **0.89**, establishing strong statistical alignment with human reviewers.
3. **Wilson Score Confidence Bounds**: For each threshold $t \in [0.70, 0.99]$, ProofGate computes the exact binomial proportion confidence interval:

$$\text{Wilson Upper}(p, n) = \frac{p + \frac{z^2}{2n} + z \sqrt{\frac{p(1-p)}{n} + \frac{z^2}{4n^2}}}{1 + \frac{z^2}{n}}$$

Rather than trusting a noisy point estimate, ProofGate automatically picks the lowest similarity threshold whose **Wilson 95% upper bound on false-hit rate is $\le 1\%$**.

---

## 3. The Empirical Results (GLUE QQP Replay)

Across 500 evaluation pairs using `nomic-embed-text`:
- **Selected 1% False-Hit Bound Threshold:** `0.86`
- **Estimated Point False-Hit Rate:** `0`
- **Wilson 95% Upper Bound:** `0.01`
- **Cache Hit Rate Achieved:** `0.12`

If an organization has higher risk tolerance and accepts up to a 5% false-hit bound, ProofGate selects threshold `0.82` (Wilson upper: `0.05`), unlocking a `0.17` hit rate.

---

## 4. How to Run It in 10 Minutes

You don't have to guess your threshold:
```bash
git clone https://github.com/proofgate/proofgate.git
cd proofgate
make quickstart
```
Set your route to `mode: shadow`, collect 500 shadow evaluations, and let ProofGate calibrate your production threshold with mathematical certainty.
