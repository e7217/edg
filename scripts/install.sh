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

echo "Installing EDG IoT Platform to $INSTALL_DIR (config: $EDG_ENV)..."

priv mkdir -p "$INSTALL_DIR"/{bin,configs,data,templates}

priv cp "$BUNDLE_DIR/edg-core" "$INSTALL_DIR/bin/"
priv cp "$BUNDLE_DIR/$VM_BIN" "$INSTALL_DIR/bin/victoria-metrics-prod"
priv chmod +x "$INSTALL_DIR/bin/edg-core" "$INSTALL_DIR/bin/victoria-metrics-prod"

priv cp -r "$BUNDLE_DIR/configs/." "$INSTALL_DIR/configs/"
priv cp -r "$BUNDLE_DIR/templates/." "$INSTALL_DIR/templates/"

# edg-core discovers <install root>/config.yaml. Without this link the configs
# above are dead files: the process finds nothing and silently runs on
# compiled-in defaults, so `auth.mode: strict` in the file it just installed
# has no effect (#117). The container image does the same thing at build time.
priv ln -sfn "$INSTALL_DIR/configs/core/config.${EDG_ENV}.yaml" "$INSTALL_DIR/config.yaml"

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
