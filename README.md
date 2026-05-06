# EDG Platform

![Status](https://img.shields.io/badge/status-pre--alpha-orange)

<div align="center">
<img src="https://github.com/user-attachments/assets/ec0ec2c0-3fa1-4ab1-bced-ed4ad549dffe" width="30%" alt="edge (1)">
</div>

> **Industrial Edge Data Gateway**
> 
> Low-overhead, high-performance edge gateway for industrial data ingestion, validation, and storage.

> [!WARNING]
> EDG is in early development and is not yet production-ready.
> APIs, configuration, and deployment workflows may change without notice.

## Why EDG?

*   **Lightweight & Fast**: Built with Go and NATS for ultra-low latency.
*   **Reliable**: Built-in data validation, auto-registration of assets, and documented JetStream durability boundaries.
*   **Plug & Play**: Simple Python adapters for reading any sensor data.
*   **Time-Series Ready**: Seamless integration with VictoriaMetrics/InfluxDB via Telegraf.

## Key Features

*   **Automatic Asset Registration**: Device discovery and metadata registration without manual configuration.
*   **Metadata Change Events**: Publishes asset and relation changes on NATS for adapters and sidecars.
*   **Data Validation**: Enforces schema and quality checks at the edge before data enters your storage.
*   **At-Least-Once Delivery**: Uses NATS JetStream for durable delivery after core publish acknowledgement. See [ADR 0001](docs/adr/0001-data-plane-reliability.md) for the exact reliability boundary.
*   **Flexible Adapters**: Easily write collectors in Python for Modbus, OPC-UA, or custom protocols.

## Quick Start

### 1. Installation
Download the latest release and run the installer:

```bash
# Linux / macOS
sudo ./install.sh
```

### 2. Start Services
```bash
sudo systemctl start edg-core
sudo systemctl start edg-telegraf
```

If you are using docker-compose, you can also start Grafana (optional) for dashboards.

### 3. Send Data
Use the Python SDK to send your first metric:

```python
import asyncio, json
import nats

async def main():
    nc = await nats.connect("nats://localhost:4222")
    
    # Send sensor data
    data = {
        "asset_id": "sensor-001",
        "values": [
            {"name": "temperature", "number": 25.5, "unit": "°C", "quality": "good"}
        ]
    }
    
    await nc.publish("platform.data.asset", json.dumps(data).encode())
    print("Data sent!")
    await nc.close()

asyncio.run(main())
```

## Architecture

![EDG Platform Architecture](docs/flow.drawio.png)

<details>
<summary>Mermaid</summary>

```mermaid
graph LR
    Sensor[Sensor] -->|Python Adapter| NATS1[NATS: Ingest]
    NATS1 -->|Stream| Core[EDG Core]
    Core -->|Validation| NATS2[NATS: Validated]
    NATS2 -->|Consumer| Telegraf
    Telegraf -->|Write| VM[VictoriaMetrics]
```

</details>

## Integrations

### Data Inputs
*   **Python SDK**: Custom adapters for any sensor.
*   **Standard Protocols**: Modbus, MQTT (Planned).

### Storage & Outputs
*   **VictoriaMetrics**: High-performance time-series storage (Recommended).
*   **InfluxDB**: v2 API compatible.
*   **NATS**: Raw stream access for other microservices.

## Roadmap

We are evolving from a data collector to a full **Bidirectional IoT Gateway**.

*   **Phase 1: Basic Control (Current)**
    *   Simple 1:1 Command/Response pattern.
    *   Secure execution of device commands via adapters.
*   **Phase 2: Advanced Logic (Planned)**
    *   Relationship-based control (Ontology).
    *   Automated sequences and conditional triggers.

## Documentation

*   **[User Guide](docs/USER_GUIDE.md)**: Detailed installation, configuration, and monitoring.
*   **[Developer Guide](docs/DEVELOPMENT.md)**: Building from source, contributing, and architecture details.
*   **[Architecture Decisions](docs/adr/README.md)**: Runtime and reliability decisions.

## Grafana (Optional)

Grafana is not included in the systemd-based release bundle today. If you deploy via docker-compose, you can run Grafana to visualize data stored in VictoriaMetrics.

- URL: http://localhost:3000
- Default user: `${GRAFANA_ADMIN_USER:-admin}`
- Default password: `${GRAFANA_ADMIN_PASSWORD:-admin}`

## LICENSE

Apache License 2.0. See [LICENSE](LICENSE) for details.
