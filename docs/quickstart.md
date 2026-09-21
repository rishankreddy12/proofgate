# ProofGate 10-Minute Quickstart

Get a complete, enterprise-grade LLM gateway running on your machine in under ten minutes with **zero external API keys required**.

---

## 1. Start the Gateway

ProofGate ships with a deterministic mock provider stack so you can explore all routing, caching, and analytics features immediately:

```bash
git clone https://github.com/proofgate/proofgate.git
cd proofgate
make quickstart
```

`make quickstart` performs the following automatically:
1. Generates an ephemeral 256-bit local Key Encryption Key (KEK).
2. Brings up PostgreSQL, Redis Stack, ClickHouse, Prometheus, Grafana, and `mockllm` providers.
3. Automatically bootstraps database schemas and runs all schema migrations.
4. Generates a live API key and saves it to `.quickstart-key`.

---

## 2. Send Your First Request

Copy the generated key and send an OpenAI-compatible completion request:

```bash
KEY=$(cat .quickstart-key)

curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Hello ProofGate!"}]
  }'
```

You will receive an OpenAI-compliant response:
```json
{
  "id": "chatcmpl-mock-12345",
  "object": "chat.completion",
  "created": 1726912800,
  "model": "mock-small",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Hello! I am a deterministic mock response from ProofGate."
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 12,
    "completion_tokens": 14,
    "total_tokens": 26
  }
}
```

### Using Standard OpenAI SDKs

No proprietary client library is needed. Point the official OpenAI SDK at ProofGate:

#### Python
```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key=open(".quickstart-key").read().strip(),
)

response = client.chat.completions.create(
    model="default",
    messages=[{"role": "user", "content": "Hello from Python!"}],
)
print(response.choices[0].message.content)
```

#### TypeScript / Node.js
```typescript
import OpenAI from "openai";
import * as fs from "fs";

const openai = new OpenAI({
  baseURL: "http://localhost:8080/v1",
  apiKey: fs.readFileSync(".quickstart-key", "utf-8").trim(),
});

const completion = await openai.chat.completions.create({
  model: "default",
  messages: [{ role: "user", content: "Hello from TypeScript!" }],
});
console.log(completion.choices[0].message.content);
```

---

## 3. View Analytics & Live Dashboards

Visit the provisioned Grafana dashboard:
- **URL:** [http://localhost:13000/d/proofgate](http://localhost:13000/d/proofgate) (or port 3000 in Kubernetes)
- **Authentication:** Anonymous viewer enabled by default.
- **Metrics Tracked:**
  - P50, P90, P99 Time to First Token (TTFT) and total latency.
  - Per-tenant cost and token throughput.
  - Exact and semantic cache hit rates.
  - Hedged request win/loss ratios.

---

## 4. Add a Real Provider (Zero-Trust Key Management)

ProofGate never accepts provider API keys in plain text CLI arguments or shell history. Secrets are sealed directly into PostgreSQL with AES-256-GCM envelope encryption:

```bash
# Store your OpenAI key securely via stdin:
echo "sk-proj-YOUR_ACTUAL_OPENAI_KEY" | go run ./cmd/proofgatectl provider-key set --provider openai

# Store your Anthropic key:
echo "sk-ant-YOUR_ACTUAL_ANTHROPIC_KEY" | go run ./cmd/proofgatectl provider-key set --provider anthropic
```

Update `deploy/proofgate.yaml` to route to your real provider:
```yaml
routes:
  - name: production
    targets:
      - provider: openai
        model: gpt-4o-mini
```

Reload the gateway configuration without restarting:
```bash
curl -X POST http://localhost:9090/admin/reload
```

---

## 5. Enable the Proof Layer

### 5.1 Safe Semantic Caching
Turn on calibrated semantic caching to eliminate false hits:
```yaml
routes:
  - name: faq
    targets:
      - provider: mock-a
        model: mock-small
    cache:
      mode: on
      exact: true
      semantic: true
      threshold: 0.86   # Calibrated via Wilson score upper bound <= 1%
      embedding_route: embed
```

### 5.2 Reversible Privacy Guardrails
Enable streaming-safe PII redaction:
```yaml
routes:
  - name: secure-chat
    guard:
      pii:
        enabled: true
        mode: redact
      injection:
        enabled: true
        threshold: 0.70
        action: block
```

### 5.3 Quality-Verified Smart Routing with Automated Rollback
```yaml
routes:
  - name: smart
    smart_route:
      mode: on
      cheap_target: mock-a/mock-small
      strong_target: mock-b/mock-large
      knn_enabled: true
```
If cheap-model quality degrades by more than 0.05 on the paired bootstrap 95% confidence interval, ProofGate automatically rolls back to strong-only routing within seconds.
