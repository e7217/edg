#!/usr/bin/env bash
#
# End-to-end storage test: the real edg-core binary against a real
# VictoriaMetrics, checked sample by sample (test/e2e).
#
# Every sink test before this used a mock HTTP server, which proves a request
# was sent, not what was stored. This run found that a redelivered reading was
# stored twice without -dedup.minScrapeInterval, and that a restarted core left
# its last batch stranded for JetStream's 30s AckWait.
#
# Usage: scripts/e2e-storage.sh [go test flags...]
# Needs Docker. EDG_E2E_VM_IMAGE overrides the VictoriaMetrics image;
# EDG_E2E_VM_NO_DEDUP=1 runs without the dedup flag to show what it prevents.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$(mktemp -d)"
trap 'rm -rf "$BIN_DIR"' EXIT

echo "==> building edg-core"
(cd "$ROOT" && go build -o "$BIN_DIR/edg-core" ./cmd/core)

echo "==> running test/e2e"
cd "$ROOT"
EDG_E2E_CORE_BIN="$BIN_DIR/edg-core" go test -tags e2e -count=1 -v ./test/e2e "$@"
