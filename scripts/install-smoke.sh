#!/usr/bin/env bash
#
# Smoke test for the release bundle and the installer.
#
# It builds a bundle with scripts/build-bundle.sh -- the same script
# release.yml uses -- unpacks it somewhere temporary, runs install.sh, and
# checks that the result is a working install rather than a pile of files.
#
# It exists because none of this had ever been executed: the release workflow
# had never run, so the packaging step copied a directory that has not existed
# since #27, and install.sh aborted on the same path. Two walls, neither
# visible from a unit test (#114, #117).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

echo "==> building edg-core"
(cd "$REPO_ROOT" && go build -o "$WORK/edg-core" ./cmd/core)

# Exercise both VictoriaMetrics naming conventions. The Windows asset has
# always been called victoria-metrics-windows-amd64-prod.exe while the
# packaging step looked for victoria-metrics-prod.exe, so that leg could never
# have produced an artifact.
for vm_name in victoria-metrics-prod victoria-metrics-linux-amd64-prod; do
  echo "==> bundling with VictoriaMetrics named '$vm_name'"
  stage="$WORK/stage-$vm_name"
  mkdir -p "$stage"
  cp "$WORK/edg-core" "$stage/edg-core"
  printf '#!/bin/sh\necho stub victoria-metrics\n' > "$stage/$vm_name"
  chmod +x "$stage/$vm_name"
  (cd "$stage" && "$REPO_ROOT/scripts/build-bundle.sh" "bundle-out") \
    || fail "build-bundle.sh rejected a bundle whose VM binary is named $vm_name"
  [ -f "$stage/bundle-out/victoria-metrics-prod" ] \
    || fail "$vm_name was not normalised to victoria-metrics-prod in the bundle"
done

BUNDLE="$WORK/stage-victoria-metrics-prod/bundle-out"

# The archive round trip matters: it is what a user actually receives, and it
# is where a symlink or a permission bit would be lost.
echo "==> archive round trip"
(cd "$(dirname "$BUNDLE")" && tar -czf "$WORK/edg-test.tar.gz" "$(basename "$BUNDLE")")
mkdir -p "$WORK/extracted"
tar -xzf "$WORK/edg-test.tar.gz" -C "$WORK/extracted"
EXTRACTED="$WORK/extracted/$(basename "$BUNDLE")"
[ -x "$EXTRACTED/install.sh" ] || fail "install.sh is not executable after the archive round trip"

echo "==> installing"
INSTALL_DIR="$WORK/opt-edg"
# No systemd in CI containers, and the test must not touch the host's units.
EDG_SKIP_SYSTEMD=1 INSTALL_DIR="$INSTALL_DIR" EDG_ENV=prod "$EXTRACTED/install.sh" > "$WORK/install.log" 2>&1 \
  || { cat "$WORK/install.log"; fail "install.sh exited non-zero"; }

echo "==> layout"
for required in bin/edg-core bin/victoria-metrics-prod configs/core/config.prod.yaml templates config.yaml; do
  [ -e "$INSTALL_DIR/$required" ] || { cat "$WORK/install.log"; fail "$required missing from the install"; }
done
[ -x "$INSTALL_DIR/bin/edg-core" ] || fail "the installed edg-core is not executable"
[ -n "$(ls -A "$INSTALL_DIR/templates")" ] || fail "templates/ was installed empty; templated asset creation would fail"

echo "==> the installed config is the one that gets loaded"
# This is the assertion behind #117. install.sh used to leave the configs where
# nothing looked for them, so the process ran on compiled-in defaults -- with
# nats.auth.mode compat while the file it had just installed said strict -- and
# said nothing about it.
[ -L "$INSTALL_DIR/config.yaml" ] || fail "config.yaml is not a link to the selected environment config"
target=$(readlink "$INSTALL_DIR/config.yaml")
case "$target" in
  *config.prod.yaml) ;;
  *) fail "config.yaml points at $target, not the prod config" ;;
esac

# Run the installed binary the way the unit does. -check-constraints opens the
# database and reads the template directory, then exits -- so it exercises
# every path the config actually names.
#
# The exit status is asserted, not discarded. An earlier version of this test
# swallowed it with `|| true` and printed OK while the binary it had just
# installed died on `mkdir /opt/edg: permission denied`.
set +e
log=$("$INSTALL_DIR/bin/edg-core" -config "$INSTALL_DIR/config.yaml" -check-constraints 2>&1)
rc=$?
set -e
[ $rc -eq 0 ] || { echo "$log"; fail "the installed binary exited $rc; the install is not usable"; }

echo "$log" | grep -q "\[Config\] loaded $INSTALL_DIR/config.yaml" \
  || { echo "$log"; fail "the installed binary did not report loading the installed config"; }
echo "$log" | grep -q "using built-in defaults" \
  && fail "the installed binary fell back to defaults despite being given a config"

echo "==> the install is hermetic: nothing was written outside INSTALL_DIR"
# The shipped prod config names an absolute root. If relocation did not rewrite
# it, the binary above would have created its database somewhere else entirely
# -- on a machine where that path is writable, silently.
grep -rn "/opt/edg" "$INSTALL_DIR/configs" \
  && fail "the installed config still names /opt/edg after relocating to $INSTALL_DIR"
[ -d "$INSTALL_DIR/data" ] || fail "no data directory under the install root; the config points elsewhere"

echo "==> re-running the installer preserves an edited config"
marker="# smoke-test-edit-do-not-remove"
printf '\n%s\n' "$marker" | tee -a "$INSTALL_DIR/configs/core/config.prod.yaml" > /dev/null
EDG_SKIP_SYSTEMD=1 INSTALL_DIR="$INSTALL_DIR" EDG_ENV=prod "$EXTRACTED/install.sh" > "$WORK/reinstall.log" 2>&1 \
  || { cat "$WORK/reinstall.log"; fail "re-running install.sh failed"; }
grep -q "$marker" "$INSTALL_DIR/configs/core/config.prod.yaml" \
  || fail "re-running install.sh discarded an operator edit to the active config"
[ -f "$INSTALL_DIR/configs/core/config.prod.yaml.new" ] \
  || fail "the shipped config was not left alongside as .new for the operator to merge"

echo "==> discovery from the install root (no -config, as a manual start would)"
set +e
log=$(cd "$INSTALL_DIR" && ./bin/edg-core -check-constraints 2>&1)
set -e
# /opt/edg/config.yaml is the only absolute entry in the search path, so a
# relocated install cannot be discovered -- it must say so rather than pretend.
echo "$log" | grep -qE "\[Config\] (loaded|no config file found)" \
  || { echo "$log"; fail "the binary said nothing about which config it resolved"; }

echo "OK: the bundle builds and installs; the installed config is the one that loads, the"
echo "    install runs entirely within its own root, and a re-install keeps operator edits."
