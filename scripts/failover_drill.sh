#!/usr/bin/env bash
# Compares failover on a slow (not failing) provider: breakers only vs SLO routing vs SLO + hedging.
set -euo pipefail
: "${PROOFGATE_KEY:?}"
OUT=bench/results/phase4; mkdir -p "$OUT"
CFG=deploy/proofgate.yaml
cp "$CFG" /tmp/proofgate.yaml.orig
reload() { for p in 9090 9091 9092; do curl -fsS -X POST "localhost:$p/admin/reload"; done; sleep 6; }

# 1) breakers only: remove SLOs and hedging
sed -e '/^slos:/,/^health:/{/^health:/!d}' -e 's/hedge: {enabled: true/hedge: {enabled: false/' /tmp/proofgate.yaml.orig > "$CFG"; reload
go run ./cmd/chaos -label "breakers only" -out "$OUT/failover-breakers-only.json"
sleep 40
# 2) SLO routing, no hedging
sed -e 's/hedge: {enabled: true/hedge: {enabled: false/' /tmp/proofgate.yaml.orig > "$CFG"; reload
go run ./cmd/chaos -label "SLO routing" -out "$OUT/failover-slo.json"
sleep 40
# 3) SLO routing + hedging
cp /tmp/proofgate.yaml.orig "$CFG"; reload
go run ./cmd/chaos -label "SLO routing + hedging" -out "$OUT/failover-slo-hedge.json"
