#!/usr/bin/env python3
"""Vibration sensor example adapter"""

import asyncio
import math
import random
import sys
import time
from pathlib import Path

# SDK import
sys.path.insert(0, str(Path(__file__).parent.parent))

from sdk import BaseAdapter, TagValue


class VibrationSensorAdapter(BaseAdapter):
    """Virtual vibration sensor adapter

    Collects velocity, acceleration, displacement, and alarm data.
    """

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._start_time = time.time()

    async def collect(self) -> list[TagValue]:
        """Collect vibration data"""
        # Simulate with sine wave + noise over time
        t = time.time() - self._start_time
        base_vibration = math.sin(t * 2 * math.pi * 0.5)  # 0.5Hz base vibration

        velocity = 2.0 + base_vibration * 1.5 + random.uniform(-0.3, 0.3)
        acceleration = 0.5 + abs(base_vibration) * 0.3 + random.uniform(-0.05, 0.05)
        displacement = 50 + base_vibration * 20 + random.uniform(-5, 5)

        # Alarm if velocity exceeds 3.5mm/s
        alarm = velocity > 3.5

        return [
            TagValue(
                name="velocity",
                number=round(velocity, 2),
                unit="mm/s",
            ),
            TagValue(
                name="acceleration",
                number=round(acceleration, 3),
                unit="g",
            ),
            TagValue(
                name="displacement",
                number=round(displacement, 1),
                unit="μm",
            ),
            TagValue(
                name="alarm",
                flag=alarm,
            ),
        ]


if __name__ == "__main__":
    adapter = VibrationSensorAdapter(
        asset_id="vibration-sensor-001",
        collect_interval=0.1,  # 100ms interval
        metadata={"location": "motor-1", "protocol": "virtual"},
    )
    asyncio.run(adapter.start())
