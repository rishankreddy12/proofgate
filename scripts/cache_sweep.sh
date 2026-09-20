#!/usr/bin/env bash
# Replays QQP at several semantic thresholds. Each run bumps cache.version so runs never share entries.
set -euo pipefail
export PATH="/c/Program Files/Go/bin:$PATH"
: "${PROOFGATE_KEY:?set PROOFGATE_KEY}"
CFG=deploy/proofgate.yaml
DATA=bench/datasets/qqp-replay.jsonl
OUT=bench/results/phase2
mkdir -p "$OUT"
[ -f "$DATA" ] || python bench/datasets/fetch_qqp.py --n 1000 --out "$DATA"
v=100
for th in 0.80 0.90 0.95; do
  v=$((v+1))
  # rewrites the faq route's cache line from Task 9 Step 2; the \1 and \2 groups keep the rest of the line
  sed -i -E "s/^(    cache: \{mode: \"on\", exact: true, semantic: true, )threshold: [0-9.]+(, embedding_route: embed, ttl: 1h)(, version: [0-9]+)?\}/\1threshold: $th\2, version: $v}/" "$CFG"
  grep -q "threshold: $th, embedding_route: embed, ttl: 1h, version: $v" "$CFG" || { echo "faq cache line not found in $CFG"; exit 1; }
  for p in 19090 19091 19092; do curl -fsS -X POST "localhost:$p/admin/reload"; done
  go run ./cmd/replay -url "http://localhost:18080" -file "$DATA" -route faq -label "mock-embeddings threshold=$th" -out "$OUT/qqp-mock-th$th.json"
done
