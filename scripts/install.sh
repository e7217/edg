#!/bin/bash
#
# Installs EDG from a release bundle. Run it from inside the extracted bundle:
#
#   tar -xzf edg-vX.Y.Z-linux-amd64.tar.gz
#   cd edg-vX.Y.Z-linux-amd64
#   sudo ./install.sh
#
# EDG_ENV selects which shipped config becomes the active one (dev, staging or
# prod; default prod). INSTALL_DIR relocates the install root.
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/edg}"
EDG_ENV="${EDG_ENV:-prod}"

BUNDLE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

die() { echo "install: $*" >&2; exit 1; }

# Use sudo only where it is actually needed. A system-wide install into
# /opt/edg needs root; an install into a directory the caller already owns (a
# user-local install, or the smoke test) must not, or the script cannot be
# exercised without root.
priv() {
    if [ "$(id -u)" -eq 0 ] || [ -w "$PRIV_PROBE" ]; then
        "$@"
    else
        sudo "$@"
    fi
}

# The deepest existing ancestor of INSTALL_DIR decides whether we can write.
PRIV_PROBE="$INSTALL_DIR"
while [ ! -e "$PRIV_PROBE" ] && [ "$PRIV_PROBE" != "/" ]; do
    PRIV_PROBE="$(dirname "$PRIV_PROBE")"
done

case "$EDG_ENV" in
    dev|staging|prod) ;;
    *) die "EDG_ENV must be dev, staging or prod (got '$EDG_ENV')" ;;
esac

# Check the bundle before touching the system, so a broken or partially
# extracted archive fails here rather than half way through the install.
for required in edg-core configs/core/config.${EDG_ENV}.yaml templates; do
    [ -e "$BUNDLE_DIR/$required" ] \
        || die "'$required' is missing from the bundle at $BUNDLE_DIR — extract the release archive and run this script from inside it"
done

# The VictoriaMetrics binary is not optional: the built-in sink writes to it,
# and the systemd unit below starts it. A missing one used to be swallowed,
# leaving a service whose ExecStart pointed at nothing.
VM_BIN=""
for candidate in victoria-metrics-prod victoria-metrics-prod.exe; do
    [ -f "$BUNDLE_DIR/$candidate" ] && { VM_BIN="$candidate"; break; }
done
[ -n "$VM_BIN" ] || die "no victoria-metrics-prod binary in the bundle at $BUNDLE_DIR"

# DEFAULT_ROOT is what the shipped staging and production configs hardcode.
# They are absolute on purpose: a relative data_dir would follow the working
# directory, so the same command run from elsewhere would quietly open a
# different database. The price is that relocating the install root means
# rewriting them, which is what relocate_paths does.
DEFAULT_ROOT="/opt/edg"

# relocate_paths rewrites DEFAULT_ROOT to INSTALL_DIR in a config file, using
# bash substitution rather than sed so a path containing sed metacharacters
# cannot corrupt the result.
relocate_paths() {
    local src="$1" dst="$2" line out=""
    while IFS= read -r line || [ -n "$line" ]; do
        out+="${line//$DEFAULT_ROOT/$INSTALL_DIR}"$'\n'
    done < "$src"
    printf '%s' "$out" | priv tee "$dst" > /dev/null
}

# install_configs copies the shipped configs, relocating absolute paths when
# the install root moved, and never silently overwriting a config an operator
# has edited -- the linked config.yaml points into this directory, so
# clobbering it would discard their changes with no warning.
install_configs() {
    local rel dst preserved=()
    while IFS= read -r rel; do
        dst="$INSTALL_DIR/configs/$rel"
        priv mkdir -p "$(dirname "$dst")"

        if [ -e "$dst" ]; then
            if [ "$INSTALL_DIR" != "$DEFAULT_ROOT" ]; then
                relocate_paths "$BUNDLE_DIR/configs/$rel" "$dst.new"
            else
                priv cp "$BUNDLE_DIR/configs/$rel" "$dst.new"
            fi
            if priv cmp -s "$dst" "$dst.new"; then
                priv rm -f "$dst.new"
            else
                preserved+=("configs/$rel")
            fi
            continue
        fi

        if [ "$INSTALL_DIR" != "$DEFAULT_ROOT" ]; then
            relocate_paths "$BUNDLE_DIR/configs/$rel" "$dst"
        else
            priv cp "$BUNDLE_DIR/configs/$rel" "$dst"
        fi
    done < <(cd "$BUNDLE_DIR/configs" && find . -type f | sed 's|^\./||')

    if [ ${#preserved[@]} -gt 0 ]; then
        echo ""
        echo "Kept your existing configuration. The shipped version of each file"
        echo "below is alongside it with a .new suffix; merge what you want:"
        printf '  %s\n' "${preserved[@]}"
        echo ""
    fi
}

echo "Installing EDG IoT Platform to $INSTALL_DIR (config: $EDG_ENV)..."

priv mkdir -p "$INSTALL_DIR"/{bin,configs,data,templates}

priv cp "$BUNDLE_DIR/edg-core" "$INSTALL_DIR/bin/"
priv cp "$BUNDLE_DIR/$VM_BIN" "$INSTALL_DIR/bin/victoria-metrics-prod"
priv chmod +x "$INSTALL_DIR/bin/edg-core" "$INSTALL_DIR/bin/victoria-metrics-prod"

install_configs
priv cp -r "$BUNDLE_DIR/templates/." "$INSTALL_DIR/templates/"

# edg-core discovers <install root>/config.yaml. Without this link the configs
# above are dead files: the process finds nothing and silently runs on
# compiled-in defaults, so `auth.mode: strict` in the file it just installed
# has no effect (#117). The container image does the same thing at build time.
priv ln -sfn "$INSTALL_DIR/configs/core/config.${EDG_ENV}.yaml" "$INSTALL_DIR/config.yaml"

# A relocated install that still names the default root would run split across
# two directories: binaries here, data and templates over there. Catch it now
# rather than at the first "template not found".
if [ "$INSTALL_DIR" != "$DEFAULT_ROOT" ] \
   && grep -q "$DEFAULT_ROOT" "$INSTALL_DIR/configs/core/config.${EDG_ENV}.yaml"; then
    die "config.${EDG_ENV}.yaml still refers to $DEFAULT_ROOT after relocation to $INSTALL_DIR"
fi

# Create systemd services (Linux only, and only when we can write the unit
# directory -- a user-local install has nowhere to register them).
if [ "${EDG_SKIP_SYSTEMD:-0}" = "1" ]; then
    echo "EDG_SKIP_SYSTEMD=1; not registering services."
elif command -v systemctl > /dev/null 2>&1 && { [ "$(id -u)" -eq 0 ] || command -v sudo > /dev/null 2>&1; }; then
    # -config is passed explicitly rather than relying on discovery, so the
    # unit says which configuration it runs and keeps working from any cwd.
    priv_tee() { if [ "$(id -u)" -eq 0 ]; then tee "$1" > /dev/null; else sudo tee "$1" > /dev/null; fi; }
    priv_tee /etc/systemd/system/edg-core.service <<EOF
[Unit]
Description=EDG IoT Platform Core
After=network.target

[Service]
ExecStart=$INSTALL_DIR/bin/edg-core -config $INSTALL_DIR/config.yaml
WorkingDirectory=$INSTALL_DIR
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    priv_tee /etc/systemd/system/edg-victoriametrics.service <<EOF
[Unit]
Description=EDG VictoriaMetrics
After=network.target

[Service]
ExecStart=$INSTALL_DIR/bin/victoria-metrics-prod -storageDataPath=$INSTALL_DIR/data/victoria-metrics -retentionPeriod=1y
WorkingDirectory=$INSTALL_DIR
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    if [ "$(id -u)" -eq 0 ]; then systemctl daemon-reload; else sudo systemctl daemon-reload; fi
    echo "Systemd services registered."
else
    echo "No systemd (or no privileges to register units); skipping service registration."
    echo "Start manually with:"
    echo "  $INSTALL_DIR/bin/victoria-metrics-prod -storageDataPath=$INSTALL_DIR/data/victoria-metrics &"
    echo "  $INSTALL_DIR/bin/edg-core -config $INSTALL_DIR/config.yaml &"
fi

echo "Installation complete: $INSTALL_DIR"
echo "Active config: $INSTALL_DIR/config.yaml -> configs/core/config.${EDG_ENV}.yaml"
echo ""
echo "To start services:"
echo "  sudo systemctl start edg-victoriametrics"
echo "  sudo systemctl start edg-core"
echo ""
echo "To enable services at boot:"
echo "  sudo systemctl enable edg-victoriametrics edg-core"
echo ""
echo "EDG Core writes validated data to VictoriaMetrics via its built-in sink."
echo "Inspect data at http://localhost:8428/vmui (no extra service required)."
