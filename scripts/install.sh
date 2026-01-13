#!/bin/bash
set -e

INSTALL_DIR="${INSTALL_DIR:-/opt/edg}"

echo "Installing EDG IoT Platform..."

# Create directories
sudo mkdir -p "$INSTALL_DIR"/{bin,configs,data}

# Copy binaries
sudo cp edg-core "$INSTALL_DIR/bin/"
sudo cp telegraf "$INSTALL_DIR/bin/"

# Copy configs
sudo cp -r configs/* "$INSTALL_DIR/configs/"

# Create systemd services (Linux only)
if command -v systemctl &> /dev/null; then
    # EDG Core service
    sudo tee /etc/systemd/system/edg-core.service > /dev/null <<EOF
[Unit]
Description=EDG IoT Platform Core
After=network.target

[Service]
ExecStart=$INSTALL_DIR/bin/edg-core
WorkingDirectory=$INSTALL_DIR
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    # Telegraf service
    sudo tee /etc/systemd/system/edg-telegraf.service > /dev/null <<EOF
[Unit]
Description=EDG Telegraf Agent
After=edg-core.service

[Service]
ExecStart=$INSTALL_DIR/bin/telegraf --config $INSTALL_DIR/configs/telegraf/telegraf.conf
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    sudo systemctl daemon-reload
    echo "Systemd services registered."
fi

echo "Installation complete: $INSTALL_DIR"
echo ""
echo "To start services:"
echo "  sudo systemctl start edg-core"
echo "  sudo systemctl start edg-telegraf"
