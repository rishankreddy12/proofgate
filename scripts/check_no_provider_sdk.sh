#!/usr/bin/env bash
# Enforces: adapters use net/http only (spec §2 supply-chain row, §6.2 D5).
set -euo pipefail
GOMOD="${1:-go.mod}"
PATTERN='(github\.com/openai/|github\.com/anthropics/|github\.com/sashabaranov/|google\.golang\.org/genai|github\.com/google/generative-ai-go)'
if grep -En "$PATTERN" "$GOMOD"; then
  echo "ERROR: LLM provider SDK in $GOMOD. Write a net/http adapter in internal/provider instead." >&2
  exit 1
fi
