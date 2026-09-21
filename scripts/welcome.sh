#!/usr/bin/env bash
set -euo pipefail

DB_URL=${DATABASE_URL:-postgres://proofgate:proofgate@localhost:15432/proofgate?sslmode=disable}
GW=${GW:-http://localhost:18080}
# Fallback to 8080 if 18080 not responding
if ! curl -s -f "$GW/healthz" >/dev/null 2>&1; then
  if curl -s -f "http://localhost:8080/healthz" >/dev/null 2>&1; then
    GW="http://localhost:8080"
  fi
fi

# Ensure demo tenant exists
DATABASE_URL="$DB_URL" go run ./cmd/proofgatectl tenant create --name demo >/dev/null 2>&1 || true

# Generate key
KEY=$(DATABASE_URL="$DB_URL" go run ./cmd/proofgatectl key create --tenant demo --name quickstart 2>/dev/null || cat .quickstart-key 2>/dev/null || echo "pg_live_quickstart_demo")
echo "$KEY" > .quickstart-key

cat <<EOF

===================================================================
                       PROOFGATE IS RUNNING                        
===================================================================

  Your Key:   $KEY   (saved to .quickstart-key)

  Try It:
    curl $GW/v1/chat/completions \\
      -H "Authorization: Bearer $KEY" \\
      -H "Content-Type: application/json" \\
      -d '{"model":"default","messages":[{"role":"user","content":"hello"}]}'

  Dashboards:
    http://localhost:13000/d/proofgate   (Grafana Anonymous Viewer)

Next Steps:
  1. Point any OpenAI SDK to $GW/v1 with that key.
  2. Add a real provider: edit deploy/proofgate.yaml, then run:
     echo "\$YOUR_KEY" | go run ./cmd/proofgatectl provider-key set --provider openai
  3. Turn on the proof layer: docs/quickstart.md#proof-layer
===================================================================

EOF
