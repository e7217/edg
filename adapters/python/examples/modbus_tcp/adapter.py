"""Modbus TCP adapter that plugs into BaseAdapter.

Reads each register spec independently (one round-trip per spec). This
keeps the example linear — production adapters with hundreds of tags
should batch reads by contiguous address range; that optimization is
out of scope for this reference.
"""

from __future__ import annotations

import logging

from pymodbus.client import AsyncModbusTcpClient
from pymodbus.exceptions import (
    ConnectionException,
    ModbusException,
    ModbusIOException,
)

from sdk import BaseAdapter, DeviceConnectionError, DeviceTimeoutError, TagValue

from .config import ModbusConfig
from .decoder import RegisterSpec, decode_register

logger = logging.getLogger(__name__)


class ModbusTCPAdapter(BaseAdapter):
    """Reference Modbus TCP adapter.

    Subclass or copy this for real deployments. The configuration is
    expected to come from a YAML mapping file; see config.load_config().
    """

    def __init__(self, config: ModbusConfig, **kwargs) -> None:
        super().__init__(**kwargs)
        self._cfg = config
        self._client: AsyncModbusTcpClient | None = None

    async def connect_device(self) -> None:
        self._client = AsyncModbusTcpClient(
            host=self._cfg.host,
            port=self._cfg.port,
            timeout=self._cfg.timeout,
        )
        ok = await self._client.connect()
        if not ok or not self._client.connected:
            raise DeviceConnectionError(
                f"failed to connect to modbus://{self._cfg.host}:{self._cfg.port}"
            )

    async def disconnect_device(self) -> None:
        if self._client is not None:
            self._client.close()
            self._client = None

    async def check_device_health(self) -> None:
        # Use the first register as a cheap ping. If the device dropped,
        # this read will fail and the SDK reconnect path takes over.
        if self._client is None or not self._client.connected:
            raise DeviceConnectionError("modbus client not connected")
        first = self._cfg.registers[0]
        try:
            await self._read_words(first)
        except (DeviceConnectionError, DeviceTimeoutError):
            raise
        except ModbusException as e:
            raise DeviceConnectionError(f"health check failed: {e}") from e

    async def collect(self) -> list[TagValue]:
        values: list[TagValue] = []
        for spec in self._cfg.registers:
            words = await self._read_words(spec)
            values.append(decode_register(words, spec))
        return values

    async def _read_words(self, spec: RegisterSpec) -> list[int]:
        if self._client is None:
            raise DeviceConnectionError("modbus client not initialized")

        count = spec.word_count
        try:
            if spec.function == "holding":
                resp = await self._client.read_holding_registers(
                    address=spec.address,
                    count=count,
                    device_id=self._cfg.unit_id,
                )
            elif spec.function == "input":
                resp = await self._client.read_input_registers(
                    address=spec.address,
                    count=count,
                    device_id=self._cfg.unit_id,
                )
            else:
                raise DeviceConnectionError(
                    f"unsupported function: {spec.function}"
                )
        except ConnectionException as e:
            raise DeviceConnectionError(str(e)) from e
        except ModbusIOException as e:
            raise DeviceTimeoutError(str(e)) from e

        if resp.isError():
            # Modbus exception responses (e.g. illegal address) propagate
            # here. We surface as DeviceConnectionError so the SDK's
            # backoff loop kicks in instead of crashing the adapter.
            raise DeviceConnectionError(
                f"modbus error for register {spec.name!r}: {resp}"
            )
        return list(resp.registers)
