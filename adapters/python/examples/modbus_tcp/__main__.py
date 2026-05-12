"""Runnable entry point: ``python -m modbus_tcp <mapping.yaml>``.

Defaults to mapping.yaml next to this file when no path is given.
"""

from __future__ import annotations

import asyncio
import logging
import sys
from pathlib import Path

# Allow `python examples/modbus_tcp/__main__.py` from the adapters/python
# directory without installing the package.
_THIS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(_THIS_DIR.parent.parent))  # adapters/python
sys.path.insert(0, str(_THIS_DIR.parent))         # adapters/python/examples

from modbus_tcp.adapter import ModbusTCPAdapter
from modbus_tcp.config import load_config


def _resolve_mapping_path(argv: list[str]) -> Path:
    if len(argv) > 1:
        return Path(argv[1])
    return _THIS_DIR / "mapping.yaml"


async def _run(argv: list[str]) -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    cfg = load_config(_resolve_mapping_path(argv))
    adapter = ModbusTCPAdapter(
        config=cfg,
        asset_id=f"modbus-{cfg.host}-{cfg.unit_id}",
        collect_interval=cfg.poll_interval,
        metadata={
            "protocol": "modbus-tcp",
            "host": cfg.host,
            "unit_id": str(cfg.unit_id),
        },
    )
    await adapter.start()


if __name__ == "__main__":
    try:
        asyncio.run(_run(sys.argv))
    except KeyboardInterrupt:
        pass
