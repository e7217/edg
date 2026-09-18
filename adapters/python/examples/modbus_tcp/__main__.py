"""Runnable entry point: ``python -m modbus_tcp <mapping.yaml>``.

Defaults to mapping.yaml next to this file when no path is given.
"""

from __future__ import annotations

import asyncio
import dataclasses
import logging
import os
import sys
from pathlib import Path

# Allow `python examples/modbus_tcp/__main__.py` from the adapters/python
# directory without installing the package.
_THIS_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(_THIS_DIR.parent.parent))  # adapters/python
sys.path.insert(0, str(_THIS_DIR.parent))         # adapters/python/examples

from modbus_tcp.adapter import ModbusTCPAdapter
from modbus_tcp.config import PROTOCOL_MODBUS_TCP, load_config, registers_from_points
from sdk import run_provisioned


def _resolve_mapping_path(argv: list[str]) -> Path:
    if len(argv) > 1:
        return Path(argv[1])
    return _THIS_DIR / "mapping.yaml"


async def _run(argv: list[str]) -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    cfg = load_config(_resolve_mapping_path(argv))
    nats_url = os.environ.get("EDG_NATS_URL") or cfg.nats_url
    metadata = {
        "protocol": PROTOCOL_MODBUS_TCP,
        "host": cfg.host,
        "unit_id": str(cfg.unit_id),
    }

    if cfg.provisioned:
        # The register map is the point list EDG declares for cfg.asset_id,
        # rebuilt when it changes (ADR 0011).
        def build(point_list):
            device = dataclasses.replace(cfg, registers=registers_from_points(point_list))
            return ModbusTCPAdapter(
                config=device,
                asset_id=cfg.asset_id,
                nats_url=nats_url,
                collect_interval=cfg.poll_interval,
                metadata=metadata,
            )

        await run_provisioned(cfg.asset_id, build, nats_url=nats_url)
        return

    adapter = ModbusTCPAdapter(
        config=cfg,
        asset_id=f"modbus-{cfg.host}-{cfg.unit_id}",
        nats_url=nats_url,
        collect_interval=cfg.poll_interval,
        metadata=metadata,
    )
    await adapter.start()


if __name__ == "__main__":
    try:
        asyncio.run(_run(sys.argv))
    except KeyboardInterrupt:
        pass
