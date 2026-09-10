#!/usr/bin/env bash
#
# Smoke test for the bundled docker compose stack.
#
# It exists because the stack was unable to start from a clean volume for
# months without anyone noticing (#114): the image baked a config whose data
# paths did not exist in the image, and `restart: unless-stopped` turned the
# failure into a silent crash loop. Unit tests cannot see that class of
# defect -- only actually running the shipped compose file can.
#
# Usage: scripts/compose-smoke.sh [project-name]
set -euo pipefail

PROJECT="${1:-edg-smoke}"
ENV_COPIED=""
COMPOSE_FILE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/deploy/docker/compose.yml"
READY_TIMEOUT="${READY_TIMEOUT:-180}"

compose() { docker compose -p "$PROJECT" -f "$COMPOSE_FILE" "$@"; }

cleanup() {
  local status=$?
  if [ $status -ne 0 ]; then
    echo "--- edg-core logs ---"
    compose logs --no-color --tail=80 edg-core || true
    echo "--- edg-victoriametrics logs ---"
    compose logs --no-color --tail=30 edg-victoriametrics || true
  fi
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  [ "${ENV_COPIED:-}" = "1" ] && rm -f "$(dirname "$COMPOSE_FILE")/.env"
  return $status
}
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

# container_health prints the container's Docker healthcheck state, or its
# status when it has none.
container_health() {
  local cid
  cid=$(compose ps -q edg-core 2>/dev/null || true)
  [ -n "$cid" ] || { echo "absent"; return; }
  docker inspect "$cid" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}'
}

# The stack is built with whatever a copied .env says, and .env.example steers
# ENV -- which selects the config baked into the image. Point the run at the
# example so the file we hand people is the one under test.
if [ -f "$(dirname "$COMPOSE_FILE")/.env.example" ] && [ ! -f "$(dirname "$COMPOSE_FILE")/.env" ]; then
  echo "==> using .env.example as .env for this run"
  cp "$(dirname "$COMPOSE_FILE")/.env.example" "$(dirname "$COMPOSE_FILE")/.env"
  ENV_COPIED=1
fi

echo "==> building"
compose build --quiet edg-core edg-victoriametrics

echo "==> starting"
compose up -d edg-victoriametrics edg-core

echo "==> waiting for edg-core to become healthy (timeout ${READY_TIMEOUT}s)"
deadline=$((SECONDS + READY_TIMEOUT))
while [ $SECONDS -lt $deadline ]; do
  health=$(container_health)
  [ "$health" = "healthy" ] && break
  # A crash loop is the exact failure mode of #114, and `restart:
  # unless-stopped` hides it: the container is usually back in "running" or
  # "starting" by the time we poll, so only the restart count gives it away.
  # Checking it every iteration turns a 180s timeout into a 30s verdict.
  restarts=$(docker inspect "$(compose ps -q edg-core 2>/dev/null)" --format '{{.RestartCount}}' 2>/dev/null || echo 0)
  [ "${restarts:-0}" -ge 3 ] && fail "edg-core is crash-looping (state=$health, restarts=$restarts)"
  sleep 2
done
[ "$(container_health)" = "healthy" ] || fail "edg-core never became healthy (state=$(container_health))"

echo "==> endpoints"
compose exec -T edg-core curl -fsS http://127.0.0.1:8222/healthz >/dev/null \
  || fail "/healthz did not answer"
compose exec -T edg-core curl -fsS http://127.0.0.1:8222/metrics >/dev/null \
  || fail "/metrics did not answer"

echo "==> VictoriaMetrics is scraping the core"
# The targets response is a single JSON line, so this counts occurrences with
# grep -o rather than matching lines: grep -c would return 1 no matter how many
# targets are healthy.
vm_targets_up() {
  compose exec -T edg-victoriametrics \
    wget -qO- 'http://127.0.0.1:8428/api/v1/targets' 2>/dev/null \
    | grep -o '"health":"up"' | wc -l
}
deadline=$((SECONDS + 90))
while [ $SECONDS -lt $deadline ]; do
  [ "$(vm_targets_up)" -ge 2 ] && break
  sleep 3
done
[ "$(vm_targets_up)" -ge 2 ] || fail "VictoriaMetrics does not report both scrape targets healthy"

echo "==> state is written to the mounted volume, not to the container filesystem"
# The credentials file is the cheapest witness: core creates it on first boot
# under storage.data_dir, so if data_dir is not on the volume this file is lost
# on recreate and every adapter credential silently rotates.
creds=$(compose exec -T edg-core sh -lc 'cat "$(ls /opt/edg/data/nats-credentials.json)"' 2>/dev/null) \
  || fail "no credentials file under the data volume; storage.data_dir is not where the volume is mounted"
[ -n "$creds" ] || fail "credentials file is empty"

echo "==> state survives a container recreate"
compose up -d --force-recreate edg-core
deadline=$((SECONDS + READY_TIMEOUT))
while [ $SECONDS -lt $deadline ]; do
  [ "$(container_health)" = "healthy" ] && break
  sleep 2
done
[ "$(container_health)" = "healthy" ] || fail "edg-core did not come back healthy after recreate"

creds_after=$(compose exec -T edg-core sh -lc 'cat /opt/edg/data/nats-credentials.json')
[ "$creds" = "$creds_after" ] \
  || fail "credentials changed across a recreate; the data directory is not persisted"

echo "OK: the compose stack starts, serves, scrapes and persists."
