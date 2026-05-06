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
- **Schema Migrations**: embedded migrations run automatically on startup
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

## Asset Metadata

EDG Core stores asset metadata in SQLite and exposes it through NATS metadata subjects.

Asset records include:
- `id`: stable asset identifier
- `name`: unique display name
- `template_name`: optional asset template
- `labels`: optional list of tags
- `external_ids`: optional key/value identifiers such as `irdi`, `eclass`, or `aas`
- `source`: origin tag; core uses `manual` for explicit metadata creation and `auto` for data-plane auto-registration
- `attributes`: optional free-form key/value metadata
- `created_at` and `updated_at`: creation and last metadata update timestamps

### Create Asset

Subject: `platform.meta.asset.create`

```json
{
  "name": "pump-101",
  "template_name": "vibration-sensor",
  "labels": ["line-a", "critical"],
  "external_ids": {
    "irdi": "0173-1#02-BAA120#008",
    "aas": "aas://example/pump-101"
  },
  "source": "manual",
  "attributes": {
    "manufacturer": "ACME",
    "model": "PX-10"
  }
}
```

If `source` is omitted, EDG Core stores `manual`.

### Update Asset

Subject: `platform.meta.asset.update`

The update API replaces the asset's mutable metadata fields. Send the complete desired metadata state for the asset.

```json
{
  "id": "pump-101",
  "name": "pump-101",
  "labels": ["line-a", "critical", "inspected"],
  "external_ids": {
    "aas": "aas://example/pump-101"
  },
  "source": "aas",
  "attributes": {
    "manufacturer": "ACME",
    "model": "PX-10",
    "area": "north"
  }
}
```

## Monitoring

- **NATS Monitor**: http://localhost:8222
- **VictoriaMetrics UI**: http://localhost:8428
- **Grafana** (optional, docker-compose): http://localhost:3000
- **Logs**:
  - EDG Core: `journalctl -u edg-core -f`
  - Telegraf: `journalctl -u edg-telegraf -f`
