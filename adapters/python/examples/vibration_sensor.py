#!/usr/bin/env python3
"""진동 센서 예제 어댑터"""

import asyncio
import math
import random
import sys
import time
from pathlib import Path

# SDK 임포트
sys.path.insert(0, str(Path(__file__).parent.parent))

from sdk import BaseAdapter, TagValue


class VibrationSensorAdapter(BaseAdapter):
    """가상 진동 센서 어댑터

    속도(velocity), 가속도(acceleration), 변위(displacement),
    알람(alarm) 데이터를 수집합니다.
    """

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._start_time = time.time()

    async def collect(self) -> list[TagValue]:
        """진동 데이터 수집"""
        # 시간에 따른 사인파 + 노이즈로 시뮬레이션
        t = time.time() - self._start_time
        base_vibration = math.sin(t * 2 * math.pi * 0.5)  # 0.5Hz 기본 진동

        velocity = 2.0 + base_vibration * 1.5 + random.uniform(-0.3, 0.3)
        acceleration = 0.5 + abs(base_vibration) * 0.3 + random.uniform(-0.05, 0.05)
        displacement = 50 + base_vibration * 20 + random.uniform(-5, 5)

        # 속도가 3.5mm/s 이상이면 알람
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
        collect_interval=0.5,  # 500ms 주기
        metadata={"location": "motor-1", "protocol": "virtual"},
    )
    asyncio.run(adapter.start())
