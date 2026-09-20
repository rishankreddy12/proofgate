#!/usr/bin/env bash
set -euo pipefail
export DATABASE_URL="${DATABASE_URL:-postgres://proofgate:proofgate@localhost:15432/proofgate?sslmode=disable}"
TENANT="sdk-smoke-$(date +%s)"
go run ./cmd/proofgatectl tenant create --name "$TENANT" >/dev/null
PROOFGATE_KEY="$(go run ./cmd/proofgatectl key create --tenant "$TENANT" --name sdk 2>/dev/null)"
export PROOFGATE_KEY
python e2e/sdk_smoke.py
