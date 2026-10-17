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

The Compose gateway's authenticated admin API is available at
`http://127.0.0.1:19090` (gateway-2 uses `19091`, gateway-3 uses `19092`).
The data plane remains at `http://127.0.0.1:18080` for gateway-1. These are
Compose host ports; a standalone binary with no explicit deployment config
continues to use its loopback admin default at `127.0.0.1:9090`.

---

## 4. Add a Real Provider (Zero-Trust Key Management)

ProofGate never accepts provider API keys in plain text CLI arguments or shell history. Secrets are sealed directly into PostgreSQL with AES-256-GCM envelope encryption:

First bootstrap and log in to the authenticated admin API as described in
[Remote Control-Plane & Admin CLI](#6-remote-control-plane--admin-cli). Then
configure the provider to use the database credential source:

```bash
# The key is read from stdin and sent to the authenticated control plane.
printf '%s\n' "$OPENAI_API_KEY" | go run ./cmd/proofgatectl --profile local \
  provider credential set --provider openai

# The same flow works for Anthropic or another configured provider.
printf '%s\n' "$ANTHROPIC_API_KEY" | go run ./cmd/proofgatectl --profile local \
  provider credential set --provider anthropic
```

Update the provider and route definitions in `deploy/proofgate.yaml` (or your
deployment's equivalent). `api_key_db: true` is required for the running
gateway to resolve the sealed credential:

```yaml
providers:
  - name: openai
    type: openai
    base_url: "https://api.openai.com/v1"
    api_key_db: true

routes:
  - name: production
    targets:
      - provider: openai
        model: gpt-4o-mini

  # Real embedding target used by semantic cache routes.
  - name: embed-real
    embeddings: true
    targets:
      - provider: openai
        model: text-embedding-3-small

pricing:
  openai/gpt-4o-mini: {input: 0.15, output: 0.60}
  openai/text-embedding-3-small: {input: 0.02, output: 0.00}
```

Point every semantic-cache route at `embedding_route: embed-real`, then
reload the gateway configuration without restarting:

```bash
go run ./cmd/proofgatectl --profile local config reload
```

The source-resolution code already supports `api_key_db`; the deployment
integrator must wire the provider's database key resolver and ensure the
configured KEK and PostgreSQL schema are available to the gateway.

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

---

## 6. Remote Control-Plane & Admin CLI

Manage gateway providers, configuration, sessions, and health remotely with `proofgatectl`:

### 6.1 Enable Admin Authentication

Add to `deploy/proofgate.yaml`:
```yaml
admin_auth:
  enabled: true
```

### 6.2 Bootstrap Admin User

Create the initial administrator directly on the server:
```bash
# Compose publishes PostgreSQL on 15432. Use the actual DSN for a standalone
# deployment instead.
DATABASE_URL='postgres://proofgate:proofgate@127.0.0.1:15432/proofgate?sslmode=disable' \
  go run ./cmd/proofgatectl bootstrap-admin --username admin
```

`bootstrap-admin` is intentionally a direct-database one-time operation and
is allowed only while no admin users exist.

### 6.3 Log In & Manage

Configure the profile and authenticate:
```bash
go run ./cmd/proofgatectl profile add --name local --server http://127.0.0.1:19090
go run ./cmd/proofgatectl profile use --name local
go run ./cmd/proofgatectl login

# Inspect running status and providers remotely:
go run ./cmd/proofgatectl status
go run ./cmd/proofgatectl provider list
go run ./cmd/proofgatectl health
```

For a standalone process using the default loopback admin binding, use
`http://127.0.0.1:9090` instead. The login endpoint is
`POST /admin/auth/login`; all control-plane commands use the saved bearer
session, while OpenAI-compatible traffic uses the data-plane URL
`http://127.0.0.1:18080/v1` in Compose (or `http://127.0.0.1:8080/v1` for a
standalone process).

See the full [Admin CLI & Control-Plane Guide](admin-cli.md) for complete command documentation and RBAC configurations.

