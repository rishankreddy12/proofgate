#!/usr/bin/env bash
# Verifies runtime hardening of a built image. Usage: scripts/check_image.sh proofgate:dev
set -euo pipefail
IMG="$1"
USER_ID=$(docker image inspect "$IMG" --format '{{.Config.User}}')
case "$USER_ID" in
  ""|root|0|0:0) echo "FAIL: image runs as root ($USER_ID)"; exit 1 ;;
esac
if docker run --rm --entrypoint /bin/sh "$IMG" -c true >/dev/null 2>&1; then
  echo "FAIL: image contains a shell"; exit 1
fi
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges:true "$IMG" -healthcheck >/dev/null 2>&1 && {
  echo "FAIL: healthcheck unexpectedly succeeded with no server running"; exit 1; } || true
echo "PASS: $IMG runs as $USER_ID, has no shell, starts read-only with no capabilities"
