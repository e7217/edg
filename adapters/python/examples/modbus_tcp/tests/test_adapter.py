"""Integration test for ModbusTCPAdapter against an in-process server.

We boot pymodbus's own TCP server with a known register bank, then drive
the adapter's collect() once and verify the decoded TagValues line up
with the bank we pre-loaded. This is what catches integration bugs that
unit tests on the decoder alone would miss (e.g. wrong function code,
off-by-one address, awaiting the wrong return type).
"""

from __future__ import annotations

import asyncio
import socket
import sys
from contextlib import asynccontextmanager
from pathlib import Path

import pytest
import pytest_asyncio

_EXAMPLE_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(_EXAMPLE_DIR.parent.parent))  # adapters/python
sys.path.insert(0, str(_EXAMPLE_DIR.parent))         # adapters/python/examples

from pymodbus.server import ServerAsyncStop, StartAsyncTcpServer
from pymodbus.simulator import DataType, SimData, SimDevice

from modbus_tcp.adapter import ModbusTCPAdapter
from modbus_tcp.config import ModbusConfig
from modbus_tcp.decoder import RegisterSpec


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _as_signed(u: int) -> int:
    """pymodbus's REGISTERS DataType packs values as int16; convert
    unsigned 16-bit so the wire bytes match the original pattern."""
    return u - 0x10000 if u > 0x7FFF else u


@asynccontextmanager
async def _modbus_server(port: int, *, register_seed: dict[int, int]):
    # Use the shared-block form: one SimData covers reads from any
    # register function code. Sparse seeds are materialized into a
    # contiguous bank so each address in the request range is defined.
    size = max(register_seed) + 1 if register_seed else 1
    bank = [0] * size
    for addr, val in register_seed.items():
        bank[addr] = _as_signed(val)
    shared = [SimData(address=0, count=size, values=bank, datatype=DataType.REGISTERS)]
    device = SimDevice(id=1, simdata=shared)
    task = asyncio.create_task(
        StartAsyncTcpServer(device, address=("127.0.0.1", port))
    )
    # Wait until the listener is actually accepting.
    for _ in range(50):
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.1):
                break
        except OSError:
            await asyncio.sleep(0.05)
    else:
        task.cancel()
        raise RuntimeError("modbus test server did not come up")

    try:
        yield
    finally:
        await ServerAsyncStop()
        task.cancel()
        try:
            await task
        except (asyncio.CancelledError, Exception):
            pass


@pytest.mark.asyncio
async def test_adapter_reads_and_decodes_registers() -> None:
    port = _free_port()
    # Shared register bank — both holding and input register reads at
    # the same address return the same value. Suffices for testing
    # because every spec we exercise uses a distinct address.
    register_seed = {
        10: 250,          # int16, scale 0.1 -> 25.0
        20: 0x4048,       # float32 ABCD: high word of 3.14
        21: 0xF5C3,       # float32 ABCD: low word of 3.14
        5: 0xFFFF,        # int16 negative -> -1.0
    }

    cfg = ModbusConfig(
        host="127.0.0.1",
        port=port,
        unit_id=1,
        poll_interval=1.0,
        timeout=1.0,
        registers=[
            RegisterSpec(
                name="temperature",
                function="holding",
                address=10,
                type="int16",
                scale=0.1,
                unit="°C",
            ),
            RegisterSpec(
                name="pressure",
                function="holding",
                address=20,
                type="float32",
                word_order="ABCD",
                unit="bar",
            ),
            RegisterSpec(
                name="status",
                function="input",
                address=5,
                type="int16",
            ),
        ],
    )

    async with _modbus_server(port, register_seed=register_seed):
        adapter = ModbusTCPAdapter(
            config=cfg,
            asset_id="modbus-test",
            collect_interval=cfg.poll_interval,
        )
        try:
            await adapter.connect_device()
            values = await adapter.collect()
        finally:
            await adapter.disconnect_device()

    by_name = {v.name: v for v in values}
    assert by_name["temperature"].number == pytest.approx(25.0)
    assert by_name["temperature"].unit == "°C"
    assert by_name["pressure"].number == pytest.approx(3.14, rel=1e-4)
    assert by_name["status"].number == -1.0
