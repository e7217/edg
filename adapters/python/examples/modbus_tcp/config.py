"""Mapping YAML loader for the Modbus TCP example.

The YAML schema is intentionally narrow and operator-friendly: a host
and port at the top, plus a flat list of register specs. Schema-breaking
changes bump ``version`` so users can be told to migrate explicitly.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

from .decoder import RegisterSpec


_SUPPORTED_VERSIONS = {1}
_SUPPORTED_FUNCTIONS = {"holding", "input"}
_SUPPORTED_TYPES = {"uint16", "int16", "uint32", "int32", "float32"}
_SUPPORTED_WORD_ORDERS = {"ABCD", "CDAB", "BADC", "DCBA"}


class ConfigError(ValueError):
    """Raised when the mapping YAML does not match the expected schema."""


@dataclass
class ModbusConfig:
    """Top-level Modbus TCP adapter configuration."""

    host: str
    port: int = 502
    unit_id: int = 1
    poll_interval: float = 1.0
    timeout: float = 1.0
    registers: list[RegisterSpec] = field(default_factory=list)
    # With asset_id set and no registers, the register map is the point list
    # EDG declares for that asset (ADR 0011). The device connection above
    # stays local: it is a fact about this box's network.
    asset_id: str = ""
    # EDG Core's NATS address, credentials included. EDG_NATS_URL overrides it.
    nats_url: str = "nats://localhost:4222"

    @property
    def provisioned(self) -> bool:
        return bool(self.asset_id) and not self.registers


def load_config(path: str | Path) -> ModbusConfig:
    raw = yaml.safe_load(Path(path).read_text())
    if not isinstance(raw, dict):
        raise ConfigError("top-level YAML must be a mapping")

    version = raw.get("version", 1)
    if version not in _SUPPORTED_VERSIONS:
        raise ConfigError(
            f"unsupported config version: {version} "
            f"(supported: {sorted(_SUPPORTED_VERSIONS)})"
        )

    if "host" not in raw:
        raise ConfigError("'host' is required")

    asset_id = str(raw.get("asset_id") or "")
    registers_raw = raw.get("registers")
    if registers_raw is None and asset_id:
        registers_raw = []
    elif not isinstance(registers_raw, list) or not registers_raw:
        raise ConfigError(
            "'registers' must list at least one entry, "
            "or 'asset_id' must name an asset whose point list to poll"
        )

    registers = [_parse_register(i, item) for i, item in enumerate(registers_raw)]

    return ModbusConfig(
        host=str(raw["host"]),
        port=int(raw.get("port", 502)),
        unit_id=int(raw.get("unit_id", 1)),
        poll_interval=float(raw.get("poll_interval", 1.0)),
        timeout=float(raw.get("timeout", 1.0)),
        registers=registers,
        asset_id=asset_id,
        nats_url=str(raw.get("nats_url") or "nats://localhost:4222"),
    )


PROTOCOL_MODBUS_TCP = "modbus-tcp"


def registers_from_points(point_list: Any) -> list[RegisterSpec]:
    """Turn a declared point list into a register map.

    The point's address is the register number and its encoding carries what
    a mapping.yaml register carries: function, type, word_order and scale.
    One bad point rejects the whole list rather than being skipped: a register
    map with a hole in it polls successfully and reports nothing about the hole.
    """
    if point_list.protocol and point_list.protocol != PROTOCOL_MODBUS_TCP:
        raise ConfigError(
            f"point list protocol is {point_list.protocol!r}; "
            f"this adapter reads {PROTOCOL_MODBUS_TCP!r}"
        )
    registers = []
    for index, point in enumerate(point_list.enabled_points()):
        try:
            address = int(point.address)
        except ValueError:
            raise ConfigError(
                f"point {point.name!r}: address {point.address!r} is not a register number"
            ) from None
        encoding = point.encoding or {}
        if "type" not in encoding:
            raise ConfigError(f"point {point.name!r}: encoding.type is required")
        scale = encoding.get("scale", 1.0)
        if isinstance(scale, bool) or not isinstance(scale, (int, float)):
            raise ConfigError(f"point {point.name!r}: encoding.scale must be a number")
        item = {
            "name": point.name,
            "function": encoding.get("function", "holding"),
            "address": address,
            "type": encoding["type"],
            "word_order": encoding.get("word_order", "ABCD"),
            "scale": scale,
            "unit": point.unit,
        }
        try:
            registers.append(_parse_register(index, item))
        except ConfigError as e:
            raise ConfigError(f"point {point.name!r}: {e}") from None
        if not 0 <= address <= 0xFFFF:
            raise ConfigError(f"point {point.name!r}: address {address} is out of range")
    return registers


def _parse_register(index: int, item: Any) -> RegisterSpec:
    if not isinstance(item, dict):
        raise ConfigError(f"registers[{index}]: must be a mapping")

    for key in ("name", "function", "address", "type"):
        if key not in item:
            raise ConfigError(f"registers[{index}]: missing required field '{key}'")

    function = item["function"]
    if function not in _SUPPORTED_FUNCTIONS:
        raise ConfigError(
            f"registers[{index}]: function '{function}' not supported "
            f"(expected one of {sorted(_SUPPORTED_FUNCTIONS)})"
        )

    type_ = item["type"]
    if type_ not in _SUPPORTED_TYPES:
        raise ConfigError(
            f"registers[{index}]: type '{type_}' not supported "
            f"(expected one of {sorted(_SUPPORTED_TYPES)})"
        )

    word_order = item.get("word_order", "ABCD")
    if word_order not in _SUPPORTED_WORD_ORDERS:
        raise ConfigError(
            f"registers[{index}]: word_order '{word_order}' not supported "
            f"(expected one of {sorted(_SUPPORTED_WORD_ORDERS)})"
        )

    return RegisterSpec(
        name=str(item["name"]),
        function=function,
        address=int(item["address"]),
        type=type_,
        word_order=word_order,
        scale=float(item.get("scale", 1.0)),
        unit=str(item.get("unit", "")),
    )
