#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CHK="$ROOT/scripts/check_pinned_actions.sh"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/.github/workflows"
cat > "$TMP/.github/workflows/ok.yml" <<'EOF'
jobs:
  a:
    steps:
      - uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683 # v4.2.2
      - uses: ./local-action
      - uses: docker://alpine@sha256:0000000000000000000000000000000000000000000000000000000000000000
EOF
"$CHK" "$TMP" || { echo "FAIL: pinned workflow rejected"; exit 1; }
cat > "$TMP/.github/workflows/bad.yml" <<'EOF'
jobs:
  a:
    steps:
      - uses: aquasecurity/trivy-action@master
EOF
if "$CHK" "$TMP"; then echo "FAIL: branch reference allowed"; exit 1; fi
echo PASS
