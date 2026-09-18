#!/usr/bin/env bash
#
# Capacity baseline: the real edg-core binary with its built-in sink writing to
# a real VictoriaMetrics, driven by edg-loadgen through the Go SDK.
#
# Usage: scripts/capacity.sh [results.jsonl]
#   LADDER="assets:tags:interval ..."   scenarios to run (default below)
#   DURATION=60s                         send time per scenario
#   VM_IMAGE=victoriametrics/victoria-metrics:v1.133.0
#
# Each scenario runs against a fresh core and a fresh VictoriaMetrics so one
# step's backlog cannot bleed into the next. Needs Docker.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-$ROOT/capacity-results.jsonl}"
DURATION="${DURATION:-60s}"
VM_IMAGE="${VM_IMAGE:-victoriametrics/victoria-metrics:v1.133.0}"
LADDER="${LADDER:-10:20:1s 100:20:1s 100:50:1s 250:40:1s 500:40:1s 1000:40:1s}"
WORK="$(mktemp -d)"
VM_NAME="edg-capacity-vm-$$"
CORE_PID=""

cleanup() {
  [ -n "$CORE_PID" ] && kill "$CORE_PID" 2>/dev/null && wait "$CORE_PID" 2>/dev/null || true
  docker rm -f "$VM_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "==> building"
(cd "$ROOT" && go build -o "$WORK/edg-core" ./cmd/core)
(cd "$ROOT/adapters/go/sdk" && go build -o "$WORK/edg-loadgen" ./cmd/edg-loadgen)

free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'; }

for step in $LADDER; do
  IFS=: read -r assets tags interval <<<"$step"
  label="${assets}x${tags}@${interval}"
  echo "==> $label"

  docker rm -f "$VM_NAME" >/dev/null 2>&1 || true
  vm_port=$(free_port)
  docker run -d --name "$VM_NAME" -p "127.0.0.1:$vm_port:8428" "$VM_IMAGE" \
    -retentionPeriod=100y -dedup.minScrapeInterval=1ms >/dev/null
  until curl -sf "http://127.0.0.1:$vm_port/health" >/dev/null; do sleep 0.2; done

  data="$WORK/data-$label"; mkdir -p "$data"
  nats_port=$(free_port); mon_port=$(free_port); metrics_port=$(free_port)
  cat > "$WORK/core.yaml" <<YAML
nats: {host: 127.0.0.1, port: $nats_port, http_host: 127.0.0.1, http_port: $mon_port,
       auth: {mode: compat}, store_dir: $data/jetstream, log_level: info}
storage: {metadata_db: $data/metadata.db, data_dir: $data, migrations_dir: embedded, auto_migrate: true}
templates: {dir: $WORK/none}
metrics: {enabled: true, address: 127.0.0.1:$metrics_port}
sink: {enabled: true, url: "http://127.0.0.1:$vm_port", consumer_stat_interval: 1s}
YAML
  "$WORK/edg-core" -config "$WORK/core.yaml" > "$WORK/core-$label.log" 2>&1 &
  CORE_PID=$!
  until curl -sf "http://127.0.0.1:$metrics_port/metrics" >/dev/null; do sleep 0.2; done

  "$WORK/edg-loadgen" -url "nats://127.0.0.1:$nats_port" -metrics "http://127.0.0.1:$metrics_port/metrics" \
    -assets "$assets" -tags "$tags" -interval "$interval" -duration "$DURATION" \
    -label "$label" -json "$OUT"

  kill "$CORE_PID"; wait "$CORE_PID" 2>/dev/null || true; CORE_PID=""
done

echo "==> results appended to $OUT"
