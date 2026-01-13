#!/usr/bin/env python3
"""온도 센서 예제 어댑터"""

import asyncio
import random
import sys
from pathlib import Path

# SDK 임포트 (개발 환경에서는 상대 경로 사용)
sys.path.insert(0, str(Path(__file__).parent.parent))

from sdk import BaseAdapter, TagValue


class TempSensorAdapter(BaseAdapter):
    """가상 온도/습도 센서 어댑터"""

    async def collect(self) -> list[TagValue]:
        """온도와 습도 데이터 수집"""
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
