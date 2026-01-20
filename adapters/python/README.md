# EDG SDK

Python SDK for EDG Platform - Adapter Development Kit

## Installation

```bash
uv sync
```

## Quick Start

```python
from sdk import BaseAdapter, TagValue

class MySensorAdapter(BaseAdapter):
    async def collect(self) -> list[TagValue]:
        return [
            TagValue(name="temperature", number=25.5, unit="°C"),
        ]

adapter = MySensorAdapter(asset_id="sensor-001")
asyncio.run(adapter.start())
```

## Features

### Device Connection Recovery

The SDK provides automatic retry and reconnection capabilities for unreliable device connections:

- **Automatic Retry**: Failed connections are automatically retried with exponential backoff
- **Device State Tracking**: Monitor connection state (DISCONNECTED, CONNECTING, CONNECTED, RECONNECTING, ERROR)
- **Event Hooks**: React to connection events (connected, disconnected, reconnected)
- **Health Monitoring**: Periodic health checks with automatic recovery
- **Configurable Backoff**: Exponential backoff with jitter to prevent thundering herd

```python
from sdk import BaseAdapter, TagValue, DeviceConnectionError

class MyDeviceAdapter(BaseAdapter):
    async def connect_device(self):
        """Override to connect to your physical device"""
        self.device = await SomeDevice.connect()

    async def disconnect_device(self):
        """Override to disconnect from device"""
        if self.device:
            await self.device.close()

    async def check_device_health(self):
        """Override to implement health checks"""
        if not await self.device.ping():
            raise DeviceConnectionError("Device not responding")

    async def collect(self) -> list[TagValue]:
        """Collect data from device"""
        data = await self.device.read()
        return [TagValue(name="value", number=data)]

    async def on_device_connected(self):
        """Called when device connects successfully"""
        print(f"Device {self.asset_id} connected!")

    async def on_device_disconnected(self, error=None):
        """Called when device disconnects"""
        if error:
            print(f"Device disconnected: {error}")
```

**Device States:**
- `DISCONNECTED`: Device not connected
- `CONNECTING`: Initial connection in progress
- `CONNECTED`: Device connected and operational
- `RECONNECTING`: Attempting to reconnect after failure
- `ERROR`: Max retries exceeded, manual intervention needed

**Retry Behavior:**
- Default: 5 max retries with exponential backoff (1s, 2s, 4s, 8s, 16s)
- Only retries `DeviceConnectionError` and `DeviceTimeoutError`
- Other exceptions are logged but not retried
- Automatic backoff with 10% jitter to prevent synchronized retries

### NATS Connection Configuration

Configure NATS connection behavior with reconnection and timeout settings:

```python
adapter = MySensorAdapter(
    asset_id="sensor-001",
    nats_url="nats://localhost:4222",
    nats_max_reconnect_attempts=-1,     # Unlimited reconnection attempts (default: -1)
    nats_reconnect_time_wait=2.0,       # Wait time between reconnects in seconds (default: 2.0)
    nats_connect_timeout=2.0,           # Connection timeout in seconds (default: 2.0)
)
```

**NATS Connection Features:**
- **Unlimited Reconnection**: Continuously retries connection on network loss (default)
- **Configurable Retry**: Adjust max attempts and wait time between retries
- **Connection Timeout**: Control how long to wait for initial connection
- **Callbacks**: Automatic logging on disconnect/reconnect events

**Default Behavior:**
- **Unlimited reconnection attempts** (-1 = retry forever, suitable for long-running adapters)
- 2 second wait between reconnection attempts
- 2 second connection timeout
- Automatic reconnection on network interruptions

**Production Recommendation:**
- Keep default `-1` for unlimited reconnection (adapters should always reconnect)
- Set finite limit only for testing or temporary adapters
- Use `nats_reconnect_time_wait` to control backoff between retries

## Examples

### Simple Usage (No Device Recovery)

Basic adapter usage without device recovery features:

```bash
# Simple temperature/humidity sensor
uv run examples/temp_sensor.py

# Vibration monitoring sensor
uv run examples/vibration_sensor.py
```

These examples show minimal adapter implementation - just override `collect()` method.

### Advanced Usage (With Device Recovery)

Full device recovery features with automatic retry and reconnection:

```bash
# Temperature sensor with device recovery
uv run examples/temp_sensor_with_recovery.py
```

This example demonstrates:
- **Connection retry** with exponential backoff (automatic retry on connection failures)
- **Automatic reconnection** on device errors during operation
- **Event hooks** for connection lifecycle (on_device_connected, on_device_disconnected, on_device_reconnected)
- **Health check monitoring** before each data collection
- **State tracking** throughout all operations (DISCONNECTED → CONNECTING → CONNECTED)
- **Error handling** with distinction between recoverable and non-recoverable errors
