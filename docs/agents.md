# Agent Governance & MCP Proxy Guide

ProofGate provides native governance for autonomous AI agents, multi-turn reasoning loops, and Model Context Protocol (MCP) tool execution.

---

## 1. Creating Agent-Governed API Keys

Provision keys with strict step counts, cost caps, token limits, and run ID enforcement:

```bash
# Create key with $0.50 budget, 30 max steps, and mandatory run IDs
proofgatectl key create \
  --tenant acme \
  --name agent-worker \
  --routes default \
  --run-max-cost-usd 0.50 \
  --run-max-steps 30 \
  --run-max-tokens 50000 \
  --require-run-id
```

### Run Policy Parameters
- `--run-max-cost-usd`: Maximum cumulative cost in USD for a single run ID. Subsequent requests return `402 run_budget_exceeded`.
- `--run-max-steps`: Maximum number of LLM steps or tool calls allowed for a run ID.
- `--run-max-tokens`: Total prompt + completion token limit per run.
- `--require-run-id`: Rejects requests without an `X-ProofGate-Run-Id` header with `400 invalid_request`.

---

## 2. Setting Run IDs in Client Frameworks

### OpenAI Python SDK

Pass `X-ProofGate-Run-Id` via default headers when initializing a per-run client:

```python
from openai import OpenAI
import uuid

run_id = f"agent-{uuid.uuid4()}"

client = OpenAI(
    base_url="https://proofgate.internal:8080/v1",
    api_key="pg_live_...",
    default_headers={"X-ProofGate-Run-Id": run_id},
)

# Every completion within this run increments the run step and tracks budget
response = client.chat.completions.create(
    model="default",
    messages=[{"role": "user", "content": "Analyze repository security"}],
)
```

### Response Headers
ProofGate injects governance tracking headers into every response:
- `X-ProofGate-Run-Steps`: Current step count for this run.
- `X-ProofGate-Run-Remaining-USD`: Remaining budget before termination.

---

## 3. Loop Detection

ProofGate inspects every turn's normalized fingerprint (ignoring IDs, whitespace, timestamps, and numbers):
- If an agent generates the same action 3 times within a 20-step sliding window, ProofGate immediately halts the run with:
  ```json
  {
    "error": {
      "status": 429,
      "code": "agent_loop_detected",
      "message": "run agent-123 repeated the same step 3 times in its last 20 steps"
    }
  }
  ```

---

## 4. MCP Proxy Configuration

### Define an MCP Tool Policy (`mcp-policy.json`)

Enforce deny-by-default tool access with glob matching (deny rules take precedence over allow rules):

```json
{
  "servers": {
    "github": {
      "allow": ["get_*", "list_*", "search_*", "create_issue"],
      "deny": ["*delete*", "*secret*", "*admin*"]
    },
    "filesystem": {
      "allow": ["read_file", "list_directory"],
      "deny": ["write_*", "delete_*"]
    }
  }
}
```

Assign the policy when creating an API key:
```bash
proofgatectl key create \
  --tenant acme \
  --name coding-agent \
  --mcp-policy mcp-policy.json
```

---

## 5. Connecting MCP Clients

Connect MCP clients (e.g. Claude Code, Cursor, Windsurf) directly to ProofGate's MCP reverse proxy:

```json
{
  "mcpServers": {
    "github": {
      "url": "https://proofgate.internal:8080/mcp/github",
      "headers": {
        "Authorization": "Bearer pg_live_...",
        "X-ProofGate-Run-Id": "run-456"
      }
    }
  }
}
```

### Security & Privacy Guarantees
1. **Tool Cloaking**: Disallowed tools are pruned from `tools/list` payloads on the fly; the agent model never knows unauthorized tools exist.
2. **Immediate Rejection**: Attempted calls to unauthorized tools return `-32001` JSON-RPC errors directly from the gateway in $<1\text{ms}$.
3. **Credential Isolation**: Upstream credentials configured in `deploy/proofgate.yaml` are injected by the gateway; client bearer tokens are never leaked to tool servers.
4. **Audit Trail**: Every invocation is logged to ClickHouse (`mcp_calls`) for security and compliance audits.
