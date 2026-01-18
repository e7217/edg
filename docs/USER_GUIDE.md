# EDG User Guide

This guide provides detailed instructions for installing, configuring, and monitoring the EDG platform.

## Installation Details

### Using Release Bundles

1. **Download the release bundle for your platform:**
   - Linux x86_64: `edg-vX.X.X-linux-amd64.tar.gz`
   - Linux ARM64: `edg-vX.X.X-linux-arm64.tar.gz`
   - macOS: `edg-vX.X.X-darwin-amd64.tar.gz`
   - Windows: `edg-vX.X.X-windows-amd64.zip`

2. **Extract the bundle:**
   ```bash
   tar -xzf edg-vX.X.X-linux-amd64.tar.gz
   cd edg-vX.X.X-linux-amd64
   ```

3. **Run the installation script (Linux/macOS):**
   ```bash
   sudo ./install.sh
   ```
   This will:
   - Install binaries to `/opt/edg/bin/`
   - Copy configs to `/opt/edg/configs/`
   - Create systemd services (Linux only)

### Managing Services (Systemd)

```bash
# Start services
sudo systemctl start edg-core
sudo systemctl start edg-telegraf

# Enable auto-start on boot
sudo systemctl enable edg-core
sudo systemctl enable edg-telegraf
```

### Manual Start (No Systemd)

If you are not using systemd, you can start components manually:

```bash
# Start EDG Core
/opt/edg/bin/edg-core &

# Start Telegraf
/opt/edg/bin/telegraf --config /opt/edg/configs/telegraf/telegraf.conf &
```

### Custom Installation Directory

```bash
INSTALL_DIR=/custom/path ./install.sh
```

## Configuration

### EDG Core
- **Data Storage**: `./data/metadata.db` (auto-created)
- **Templates**: `./templates/` (optional)

### Telegraf
Configuration file: `/opt/edg/configs/telegraf/telegraf.conf`

**Key Settings:**
- Input: NATS (`platform.data.validated`)
- Output: VictoriaMetrics (`http://localhost:8428`)
- Parser: `json_v2` (handles nested arrays)

**Data Format:**
Incoming JSON from adapters:
```json
{
  "asset_id": "sensor-001",
  "values": [
    {"name": "temperature", "number": 25.5, "unit": "°C", "quality": "good"}
  ]
}
```

## Monitoring

- **NATS Monitor**: http://localhost:8222
- **VictoriaMetrics UI**: http://localhost:8428
- **Grafana** (optional, docker-compose): http://localhost:3000
- **Logs**:
  - EDG Core: `journalctl -u edg-core -f`
  - Telegraf: `journalctl -u edg-telegraf -f`
