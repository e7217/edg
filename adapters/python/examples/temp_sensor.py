#!/usr/bin/env python3
"""Temperature sensor example adapter"""

import asyncio
import random
import sys
from pathlib import Path

# SDK import (using relative path in development environment)
sys.path.insert(0, str(Path(__file__).parent.parent))

from sdk import BaseAdapter, TagValue


class TempSensorAdapter(BaseAdapter):
    """Virtual temperature/humidity sensor adapter"""

    async def collect(self) -> list[TagValue]:
        """Collect temperature and humidity data"""
        return [
            TagValue(
                name="temperature",
                number=20 + random.random() * 10,  # 20~30°C
                unit="°C",
            ),
            TagValue(
                name="humidity",
                number=40 + random.random() * 30,  # 40~70%
                unit="%",
            ),
        ]


if __name__ == "__main__":
    adapter = TempSensorAdapter(
        asset_id="temp-sensor-001",
        collect_interval=1.0,
        metadata={"location": "factory-a", "protocol": "virtual"},
    )
    asyncio.run(adapter.start())
