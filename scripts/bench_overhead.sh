#!/usr/bin/env bash
# Measures gateway overhead against a deterministic mock, two ways, three repeats.
set -euo pipefail
: "${PROOFGATE_KEY:?}"
OUT=bench/results/overhead; mkdir -p "$OUT"
GW=${GW:-http://localhost:8080}
ADMIN_URL=${ADMIN_URL:-http://localhost:9090}
MOCK=${MOCK:-http://localhost:18083}
REPEATS=${REPEATS:-3}

metrics_quantiles() { # prints p50/p99 of proofgate_overhead_seconds from a fresh scrape
  curl -fsS "$ADMIN_URL/metrics" | awk '/^proofgate_overhead_seconds_bucket/ {print}' > "$1" || true
}

for scenario in "stream-200 --stream -rps 200" "stream-1000 --stream -rps 1000" "nostream-1000 -rps 1000"; do
  name=${scenario%% *}; args=${scenario#* }
  for i in $(seq 1 "$REPEATS"); do
    stream_flag=""; [[ "$args" == *"--stream"* ]] && stream_flag="-stream"
    rps=$(echo "$args" | sed -E 's/.*-rps ([0-9]+).*/\1/')
    go run ./cmd/loadgen -url "$MOCK" -route mock-small $stream_flag -rps "$rps" \
      -label "$name direct" -meta gateway=none -out "$OUT/$name-direct-$i.json"
    metrics_quantiles "$OUT/$name-gateway-$i.before.prom"
    go run ./cmd/loadgen -url "$GW" -key "$PROOFGATE_KEY" -route bench $stream_flag -rps "$rps" \
      -label "$name gateway" -meta gateway=proofgate -meta version="$(git describe --tags --always)" \
      -out "$OUT/$name-gateway-$i.json"
    metrics_quantiles "$OUT/$name-gateway-$i.after.prom"
    docker stats --no-stream --format '{"container":"{{.Name}}","mem":"{{.MemUsage}}","cpu":"{{.CPUPerc}}"}' \
      > "$OUT/$name-stats-$i.json" || true
    sleep 10
  done
done

TARGET_URL=$GW ROUTE=bench PROOFGATE_KEY=$PROOFGATE_KEY k6 run --summary-export "$OUT/saturation.json" bench/k6/chat.js || true
echo "results in $OUT"
