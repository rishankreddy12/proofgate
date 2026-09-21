#!/usr/bin/env bash
set -euo pipefail
OUT=bench/results/compare; mkdir -p "$OUT"
cd "$(dirname "$0")"
docker compose -f docker-compose.bench.yml up -d --build --wait
cd ../..
export PROOFGATE_KEY=${PROOFGATE_KEY:?}
declare -A URLS=( [proofgate]="http://localhost:8080" [litellm]="http://localhost:8091" [bifrost]="http://localhost:8092" )
declare -A KEYS=( [proofgate]="$PROOFGATE_KEY" [litellm]="sk-not-used" [bifrost]="" )
for gw in proofgate litellm bifrost; do
  digest=$(docker inspect --format '{{index .RepoDigests 0}}' "$(docker compose -f bench/compare/docker-compose.bench.yml images -q $gw)" 2>/dev/null || echo "local-build")
  for scenario in "stream-1000 -stream 1000" "nostream-1000 '' 1000"; do
    name=${scenario%% *}; rest=${scenario#* }; flag=${rest%% *}; rps=${rest##* }
    for i in 1 2 3; do
      go run ./cmd/loadgen -url "${URLS[$gw]}" -key "${KEYS[$gw]}" -route bench $flag -rps "$rps" \
        -label "$gw $name" -meta gateway="$gw" -meta image="$digest" \
        -out "$OUT/$gw-$name-$i.json" || echo "{\"label\":\"$gw $name\",\"failed\":true}" > "$OUT/$gw-$name-$i.json"
      sleep 10
    done
  done
done
docker compose -f bench/compare/docker-compose.bench.yml down -v
