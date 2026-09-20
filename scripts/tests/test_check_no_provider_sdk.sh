#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
GUARD="$ROOT/scripts/check_no_provider_sdk.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
printf 'module x\n\ngo 1.23\n\nrequire github.com/go-chi/chi/v5 v5.1.0\n' > "$TMP/go.mod"
"$GUARD" "$TMP/go.mod" || { echo "FAIL: clean go.mod rejected"; exit 1; }
for dep in github.com/openai/openai-go github.com/anthropics/anthropic-sdk-go github.com/sashabaranov/go-openai google.golang.org/genai github.com/google/generative-ai-go; do
  printf 'module x\n\ngo 1.23\n\nrequire %s v1.0.0\n' "$dep" > "$TMP/go.mod"
  if "$GUARD" "$TMP/go.mod"; then echo "FAIL: $dep allowed"; exit 1; fi
done
echo PASS
