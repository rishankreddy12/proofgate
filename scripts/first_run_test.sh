#!/usr/bin/env bash
# Measures how long a stranger needs to get a working response, from a clean clone.
set -euo pipefail
START=$(date +%s)
make quickstart
KEY=$(cat .quickstart-key)
GW=${GW:-http://localhost:18080}
if ! curl -s -f "$GW/healthz" >/dev/null 2>&1; then
  if curl -s -f "http://localhost:8080/healthz" >/dev/null 2>&1; then
    GW="http://localhost:8080"
  fi
fi

until curl -fsS "$GW/v1/chat/completions" -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
    -d '{"model":"default","messages":[{"role":"user","content":"hello"}],"max_tokens":5}' >/dev/null 2>&1; do
  [ $(( $(date +%s) - START )) -gt 600 ] && { echo "FAIL: no working response within 10 minutes"; exit 1; }
  sleep 5
done
END=$(date +%s)
mkdir -p bench/results
printf '{"seconds":%d,"runner":"%s","date":"%s","commit":"%s"}\n' $((END-START)) "${RUNNER_OS:-local}" \
  "$(date -u +%FT%TZ)" "$(git rev-parse --short HEAD)" | tee bench/results/first-run.json
