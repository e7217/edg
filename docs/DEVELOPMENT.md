# EDG Developer Guide

This guide is for developers who want to build, test, and contribute to the EDG platform.

## Prerequisites

- Go 1.21 or later
- Git

## Building from Source

```bash
# Clone the repository
git clone https://github.com/e7217/edg.git
cd edg

# Build EDG Core
go build -o edg-core ./cmd/core

# Run locally
./edg-core
```

## Local Development and Testing

### Automated Pipeline Test
Test the complete pipeline locally with a single command:

```bash
./test_pipeline.sh
```

This script will:
1. Build EDG Core
2. Download and setup VictoriaMetrics (if not installed)
3. Start all services
4. Publish test sensor data
5. Verify data flow to VictoriaMetrics

### Manual Testing

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

Core's built-in sink writes to VictoriaMetrics directly — no separate metrics
agent is needed. Point the sink at your VictoriaMetrics with the `EDG_SINK_URL`
env var (defaults to `http://localhost:8428`):

```bash
EDG_SINK_URL=http://localhost:8428 go run ./cmd/core/main.go
```

**3. Send test data:**

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

**4. Verify data in VictoriaMetrics:**
```bash
# Query the metric written by the built-in sink
curl 'http://localhost:8428/api/v1/query?query=edg_data_number' | jq '.'

# Or explore interactively in the built-in UI
open http://localhost:8428/vmui
```

## Running Unit Tests

```bash
go test ./...
```

## Project Structure

```
edg/
├── cmd/
│   └── core/           # EDG Core main entry
├── internal/
│   └── core/           # Core business logic
├── deploy/
│   ├── docker/         # Docker deployment files
│   │   ├── compose.yml
│   │   └── Dockerfile.core
│   └── configs/        # Shared deployment configs
│       └── core/       # Core YAML configs (dev/staging/prod)
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
   - Download the VictoriaMetrics binary
   - Package release bundles
   - Create GitHub release with assets
