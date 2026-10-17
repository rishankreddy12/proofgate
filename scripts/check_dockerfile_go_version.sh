#!/usr/bin/env bash
# Verifies Dockerfiles have a Go version matching or exceeding go.mod.
set -euo pipefail

GOMOD_VER=$(grep -E '^go [0-9]+' go.mod | awk '{print $2}' | cut -d. -f1,2)
if [ -z "$GOMOD_VER" ]; then
  echo "ERROR: could not determine go version from go.mod" >&2
  exit 1
fi

status=0
for df in deploy/Dockerfile deploy/Dockerfile.mockllm; do
  if [ ! -f "$df" ]; then
    continue
  fi
  DF_VER=$(grep -E 'ARG GO_VERSION=[0-9.]+' "$df" | head -1 | cut -d= -f2 || true)
  if [ -z "$DF_VER" ]; then
    DF_VER=$(grep -E 'FROM golang:[0-9.]+' "$df" | head -1 | sed -E 's/.*golang:([0-9.]+).*/\1/' || true)
  fi
  DF_MAJOR_MINOR=$(echo "$DF_VER" | cut -d. -f1,2)
  if [ -z "$DF_MAJOR_MINOR" ]; then
    echo "ERROR: could not determine Go version in $df" >&2
    status=1
    continue
  fi
  # Compare versions
  LOWEST=$(printf '%s\n%s\n' "$GOMOD_VER" "$DF_MAJOR_MINOR" | sort -V | head -1)
  if [ "$LOWEST" != "$GOMOD_VER" ] && [ "$GOMOD_VER" != "$DF_MAJOR_MINOR" ]; then
    echo "ERROR: $df uses Go $DF_MAJOR_MINOR but go.mod requires $GOMOD_VER" >&2
    status=1
  fi
done

if [ $status -eq 0 ]; then
  echo "PASS: Dockerfile Go versions align with go.mod ($GOMOD_VER)"
fi
exit $status
