#!/usr/bin/env python3
"""Temperature sensor example with device recovery features"""

import asyncio
import random
import sys
from pathlib import Path

# SDK import (using relative path in development environment)
sys.path.insert(0, str(Path(__file__).parent.parent))

from sdk import (
    BaseAdapter,
    TagValue,
    DeviceConnectionError,
    DeviceTimeoutError,
)


class TemperatureSensor:
    """Simulated temperature sensor with unreliable connection"""

    def __init__(self, fail_probability: float = 0.3):
        self.fail_probability = fail_probability
        self.connected = False
        self.connection_attempts = 0

    async def connect(self):
        """Simulate connection with initial failures"""
        self.connection_attempts += 1
        # Fail first 2 connection attempts
        if self.connection_attempts <= 2:
            raise DeviceConnectionError(
                f"Connection attempt {self.connection_attempts} failed (simulated)"
            )
        self.connected = True
        print(f"🔌 Sensor connected after {self.connection_attempts} attempts")

    async def disconnect(self):
        """Disconnect from sensor"""
        self.connected = False
        print("🔌 Sensor disconnected")

    async def ping(self) -> bool:
        """Health check with random failures"""
        if not self.connected:
            return False
        # Randomly fail health check
        if random.random() < self.fail_probability:
            return False
        return True

    async def read_temperature(self) -> float:
        """Read temperature value"""
        if not self.connected:
            raise DeviceConnectionError("Sensor not connected")
        # Randomly fail reading
        if random.random() < self.fail_probability:
            raise DeviceTimeoutError("Sensor read timeout")
        return 20.0 + random.random() * 10.0

    async def read_humidity(self) -> float:
        """Read humidity value"""
        if not self.connected:
            raise DeviceConnectionError("Sensor not connected")
        # Randomly fail reading
        if random.random() < self.fail_probability:
            raise DeviceTimeoutError("Sensor read timeout")
        return 40.0 + random.random() * 30.0


class TempSensorWithRecoveryAdapter(BaseAdapter):
    """Temperature sensor adapter with device recovery features"""

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.sensor = TemperatureSensor(fail_probability=0.2)

    async def connect_device(self):
        """Connect to temperature sensor"""
        await self.sensor.connect()

    async def disconnect_device(self):
        """Disconnect from temperature sensor"""
        await self.sensor.disconnect()

    async def check_device_health(self):
        """Check sensor health before each collection"""
        if not await self.sensor.ping():
            raise DeviceConnectionError("Sensor health check failed")

    async def on_device_connected(self):
        """Called when sensor connects successfully"""
        print(f"✅ [Event] Sensor connected (state: {self.device_state.value})")

    async def on_device_disconnected(self, error=None):
        """Called when sensor disconnects"""
        if error:
            print(f"❌ [Event] Sensor disconnected due to: {error}")
        else:
            print(f"ℹ️  [Event] Sensor disconnected normally")

    async def on_device_reconnected(self):
        """Called when sensor reconnects after failure"""
        print(f"🔄 [Event] Sensor reconnected (state: {self.device_state.value})")

    async def collect(self) -> list[TagValue]:
        """Collect temperature and humidity data"""
        temp = await self.sensor.read_temperature()
        humidity = await self.sensor.read_humidity()

        return [
            TagValue(
                name="temperature",
                number=temp,
                unit="°C",
            ),
            TagValue(
                name="humidity",
                number=humidity,
                unit="%",
            ),
        ]


if __name__ == "__main__":
    print("=" * 60)
    print("Temperature Sensor with Device Recovery Example")
    print("This example demonstrates:")
    print("  - Automatic connection retry with exponential backoff")
    print("  - Device health monitoring")
    print("  - Event hooks for connection state changes")
    print("  - Automatic reconnection on failures")
    print("=" * 60)
    print()

    adapter = TempSensorWithRecoveryAdapter(
        asset_id="temp-sensor-with-recovery",
        collect_interval=1.0,
        metadata={"location": "factory-a", "protocol": "modbus", "recovery": "enabled"},
    )
    asyncio.run(adapter.start())
