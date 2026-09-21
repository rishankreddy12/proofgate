#!/usr/bin/env bash
# Every third-party action must be pinned to a full commit SHA (LiteLLM's 2026 compromise began with a mutable action tag).
set -euo pipefail
DIR="${1:-.}"
bad=$(grep -RhoE '^\s*-?\s*uses:\s*[^ #]+' "$DIR/.github/workflows" \
  | sed -E 's/^\s*-?\s*uses:\s*//' \
  | grep -vE '^\./' \
  | grep -vE '^docker://[^@]+@sha256:[0-9a-f]{64}$' \
  | grep -vE '^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}$' || true)
if [ -n "$bad" ]; then
  echo "Unpinned actions (pin to a 40-character commit SHA):"; echo "$bad"; exit 1
fi
