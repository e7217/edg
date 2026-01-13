# EDG Platform

An open-source Industrial Edge platform for data ingestion, validation, and storage.

## Architecture

```
Sensor → Python Adapter → NATS (platform.data.asset)
    → EDG Core (validation + auto-registration)
    → NATS (platform.data.validated)
    → Telegraf (inputs.nats_consumer)
    → VictoriaMetrics (outputs.influxdb_v2)
```

### Components

- **EDG Core**: Embedded NATS server, data validation, asset auto-registration
- **Telegraf**: Time-series data collection agent (NATS → VictoriaMetrics)
- **VictoriaMetrics**: Time-series database (InfluxDB v2 compatible)
- **Python Adapters**: Sensor data collectors

## Installation

### Using Release Bundles (Recommended)

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

4. **Start the services (Linux with systemd):**
   ```bash
   sudo systemctl start edg-core
   sudo systemctl start edg-telegraf

   # Enable auto-start on boot
   sudo systemctl enable edg-core
   sudo systemctl enable edg-telegraf
   ```

5. **Manual start (without systemd):**
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

Data stored in: `./data/metadata.db` (auto-created)

Templates directory: `./templates/` (optional)

### Telegraf

Configuration: `/opt/edg/configs/telegraf/telegraf.conf`

Key settings:
- NATS input: `platform.data.validated` topic
- VictoriaMetrics output: `http://localhost:8428`
- Parser: `json_v2` with nested array support
- Tags extracted: `asset_id`, `name`, `quality`, `unit`
- Metric name: `nats_consumer_number` (from `values` array)

**Important**: The Telegraf configuration uses `json_v2` parser to handle nested JSON arrays. Each object in the `values` array becomes a separate metric with tags extracted from `name`, `quality`, and `unit` fields.

Example data structure:
```json
{
  "asset_id": "sensor-001",
  "values": [
    {"name": "temperature", "number": 25.5, "unit": "°C", "quality": "good"},
    {"name": "humidity", "number": 60.0, "unit": "%", "quality": "good"}
  ]
}
```

Resulting metrics:
- `nats_consumer_number{asset_id="sensor-001",name="temperature",unit="°C",quality="good"} = 25.5`
- `nats_consumer_number{asset_id="sensor-001",name="humidity",unit="%",quality="good"} = 60.0`

### VictoriaMetrics

Install separately: https://victoriametrics.com/

Default port: `8428` (InfluxDB v2 compatible endpoint)

## Usage

### Sending Data via Python Adapter

```python
import nats
import json
import asyncio

async def send_data():
    nc = await nats.connect("nats://localhost:4222")

    data = {
        "asset_id": "sensor-001",
        "values": [
            {
                "name": "temperature",
                "number": 25.5,
                "unit": "°C",
                "quality": "good"
            },
            {
                "name": "humidity",
                "number": 60.0,
                "unit": "%",
                "quality": "good"
            }
        ]
    }

    await nc.publish("platform.data.asset", json.dumps(data).encode())
    await nc.close()

asyncio.run(send_data())
```

### Monitoring

- **NATS Monitor**: http://localhost:8222
- **VictoriaMetrics**: http://localhost:8428
- **EDG Core Logs**: `journalctl -u edg-core -f` (systemd)
- **Telegraf Logs**: `journalctl -u edg-telegraf -f` (systemd)

## Data Flow

1. **Sensor → Adapter**: Python adapter collects sensor data
2. **Adapter → NATS**: Publishes to `platform.data.asset` topic
3. **NATS → Core**: EDG Core subscribes and validates data
4. **Core Processing**:
   - Auto-registers new assets
   - Validates data format
   - Publishes to `platform.data.validated`
5. **Core → NATS**: Validated data published to `platform.data.validated`
6. **NATS → Telegraf**: Telegraf consumes validated data
7. **Telegraf → VictoriaMetrics**: Stores time-series data

## Development

### Prerequisites

- Go 1.21 or later
- Git

### Building from Source

```bash
# Clone the repository
git clone https://github.com/yourusername/edg.git
cd edg

# Build EDG Core
go build -o edg-core ./cmd/core

# Run locally
./edg-core
```

### Local Development and Testing

#### Quick Start (Automated)

Test the complete pipeline locally with a single command:

```bash
./test_pipeline.sh
```

This script will:
1. Build EDG Core
2. Download and setup VictoriaMetrics (if not installed)
3. Download and setup Telegraf (if not installed)
4. Start all services
5. Publish test sensor data
6. Verify data flow to VictoriaMetrics

#### Manual Testing (Step-by-Step)

> ✅ **Verified**: Complete pipeline tested successfully on 2026-01-14

**1. Start EDG Core:**
```bash
go run ./cmd/core/main.go
```

**2. Start VictoriaMetrics:**
```bash
# Download (Linux x86_64) - Download to project directory for cross-platform compatibility
wget https://github.com/VictoriaMetrics/VictoriaMetrics/releases/download/v1.96.0/victoria-metrics-linux-amd64-v1.96.0.tar.gz
tar xzf victoria-metrics-linux-amd64-v1.96.0.tar.gz

# Run from project directory
./victoria-metrics-prod -storageDataPath=./victoria-metrics-data
```

> **Note**: Download binaries to the project directory instead of `/tmp/` for better cross-platform compatibility (Windows uses `%TEMP%`).

**3. Start Telegraf:**
```bash
# Download (Linux x86_64)
wget https://dl.influxdata.com/telegraf/releases/telegraf-1.29.0_linux_amd64.tar.gz
tar xzf telegraf-1.29.0_linux_amd64.tar.gz

# Run with project config
./telegraf-1.29.0/usr/bin/telegraf --config ./configs/telegraf/telegraf.conf
```

**4. Send test data:**

Python:
```python
import asyncio, json
from nats.aio.client import Client as NATS

async def main():
    nc = NATS()
    await nc.connect("nats://localhost:4222")

    data = {
        "asset_id": "sensor-001",
        "values": [
            {"name": "temperature", "number": 25.5,
             "unit": "°C", "quality": "good"}
        ]
    }

    await nc.publish("platform.data.asset", json.dumps(data).encode())
    await nc.close()

asyncio.run(main())
```

Or Go:
```go
package main
import (
    "encoding/json"
    "github.com/nats-io/nats.go"
)
func main() {
    nc, _ := nats.Connect(nats.DefaultURL)
    defer nc.Close()

    temp := 25.5
    data := map[string]interface{}{
        "asset_id": "sensor-001",
        "values": []map[string]interface{}{
            {"name": "temperature", "number": temp,
             "unit": "°C", "quality": "good"},
        },
    }

    payload, _ := json.Marshal(data)
    nc.Publish("platform.data.asset", payload)
}
```

**5. Verify data in VictoriaMetrics:**
```bash
# Query all metrics
curl 'http://localhost:8428/api/v1/query?query=nats_consumer_number' | jq '.'

# Query temperature data from specific sensor
curl 'http://localhost:8428/api/v1/query?query=nats_consumer_number{asset_id="sensor-001",name="temperature"}' | jq '.'

# Query all sensors with humidity
curl 'http://localhost:8428/api/v1/query?query=nats_consumer_number{name="humidity"}' | jq '.'

# List all available metric names
curl 'http://localhost:8428/api/v1/label/__name__/values' | jq '.'
```

#### Monitoring During Development

- **NATS Monitor**: http://localhost:8222
- **VictoriaMetrics UI**: http://localhost:8428
- **Logs**: Check console output or log files

**Expected Data Flow:**
```
Test Data → platform.data.asset
    → EDG Core (validation)
    → platform.data.validated
    → Telegraf
    → VictoriaMetrics
```

For detailed testing scenarios and troubleshooting, see [TESTING.md](TESTING.md).

### Running Unit Tests

```bash
go test ./...
```

### Project Structure

```
edg/
├── cmd/
│   └── core/           # EDG Core main entry
├── internal/
│   └── core/           # Core business logic
├── configs/
│   └── telegraf/       # Telegraf configuration
├── scripts/
│   └── install.sh      # Installation script
├── templates/          # Asset templates (optional)
└── .github/
    └── workflows/
        └── release.yml # CI/CD release automation
```

## Release Process

Releases are automated via GitHub Actions:

1. Create a version tag: `git tag v1.0.0`
2. Push the tag: `git push origin v1.0.0`
3. GitHub Actions will:
   - Build EDG Core for all platforms
   - Download Telegraf binaries
   - Package release bundles
   - Create GitHub release with assets

## License

[Your License Here]

## Contributing

Contributions welcome! Please open an issue or pull request.
