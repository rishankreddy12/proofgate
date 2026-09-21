#!/usr/bin/env bash
set -euo pipefail
export DATABASE_URL=${DATABASE_URL:-postgres://proofgate:proofgate@localhost:5432/proofgate?sslmode=disable}
mkdir -p bench/results/phase4
T="agents-$(date +%s)"
go run ./cmd/proofgatectl tenant create --name "$T" >/dev/null
export LOOP_KEY=$(go run ./cmd/proofgatectl key create --tenant "$T" --name loop --run-max-steps 50 2>/dev/null)
# mock-small costs 1*0.1 + 16*0.4 micro-USD per call at most; 0.00003 USD stops the run after a handful of steps
export BUDGET_KEY=$(go run ./cmd/proofgatectl key create --tenant "$T" --name budget --run-max-cost-usd 0.00003 2>/dev/null)
go run ./examples/runaway-agent
