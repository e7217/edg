# Onto SDK

Python SDK for Onto Platform - Adapter Development Kit

## Installation

```bash
uv sync
```

## Usage

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

## Examples

```bash
python examples/temp_sensor.py
python examples/vibration_sensor.py
```
