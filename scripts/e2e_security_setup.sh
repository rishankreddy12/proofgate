#!/usr/bin/env bash
set -euo pipefail
export DATABASE_URL=${DATABASE_URL:-postgres://proofgate:proofgate@localhost:15432/proofgate?sslmode=disable}
for p in mock-a mock-b; do
  echo "sk-e2e-mock-provider-key-0123456789" | go run ./cmd/proofgatectl provider-key set --provider "$p" --actor e2e \
    --config deploy/proofgate.yaml.hostkek
done
for port in 19090 19091 19092 9090 9091 9092; do curl -fsS -X POST "http://localhost:$port/admin/secrets/purge" >/dev/null 2>&1 || true; done
