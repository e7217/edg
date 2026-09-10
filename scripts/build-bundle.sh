#!/usr/bin/env bash
#
# Assembles a release bundle directory from the repository.
#
#   scripts/build-bundle.sh <dist-dir> [ext]
#
# The caller places the already-built edg-core and the VictoriaMetrics binary
# in the working directory first; this script only assembles and verifies.
#
# It exists so that .github/workflows/release.yml and scripts/install-smoke.sh
# build the bundle the same way. When they were separate, the packaging step
# copied a `configs/` directory that had not existed since #27 and nothing
# noticed, because no release had ever been cut (#114, #117).
set -euo pipefail

DIST_DIR="${1:?usage: build-bundle.sh <dist-dir> [ext]}"
EXT="${2:-}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

die() { echo "build-bundle: $*" >&2; exit 1; }

mkdir -p "$DIST_DIR"

[ -f "edg-core${EXT}" ] || die "edg-core${EXT} not found in $(pwd); build it first"
cp "edg-core${EXT}" "$DIST_DIR/"

# The VictoriaMetrics release names its binary differently per platform: the
# tarballs hold `victoria-metrics-prod`, the Windows zip holds
# `victoria-metrics-windows-amd64-prod.exe`. Match either.
vm=$(find . -maxdepth 1 -type f -name "victoria-metrics*prod${EXT}" | head -1)
[ -n "$vm" ] || die "no victoria-metrics*prod${EXT} in $(pwd); found: $(ls -1 | tr '\n' ' ')"
cp "$vm" "$DIST_DIR/victoria-metrics-prod${EXT}"

# install.sh expects these beside itself.
cp -r "$REPO_ROOT/deploy/configs" "$DIST_DIR/configs"
cp -r "$REPO_ROOT/templates" "$DIST_DIR/templates"
cp "$REPO_ROOT/scripts/install.sh" "$DIST_DIR/"
cp "$REPO_ROOT/README.md" "$DIST_DIR/"
cp "$REPO_ROOT/THIRD_PARTY_LICENSES.md" "$DIST_DIR/"

# Verify before shipping: a rename upstream should fail the build, not produce
# an installer that dies on a user's machine.
for required in \
    "edg-core${EXT}" \
    "victoria-metrics-prod${EXT}" \
    "install.sh" \
    "configs/core/config.prod.yaml" \
    "configs/core/config.dev.yaml" \
    "templates"; do
    [ -e "$DIST_DIR/$required" ] || die "bundle is missing $required"
done
[ -n "$(ls -A "$DIST_DIR/templates")" ] || die "bundle ships an empty templates/ directory"

echo "build-bundle: assembled $DIST_DIR"
