"""Mapping YAML loader tests."""

from __future__ import annotations

import sys
from pathlib import Path
from textwrap import dedent

import pytest

_EXAMPLE_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(_EXAMPLE_DIR.parent.parent))  # adapters/python
sys.path.insert(0, str(_EXAMPLE_DIR.parent))         # adapters/python/examples

from modbus_tcp.config import ConfigError, ModbusConfig, load_config


def _write(tmp_path: Path, content: str) -> Path:
    p = tmp_path / "mapping.yaml"
    p.write_text(dedent(content).lstrip())
    return p


def test_load_minimal_config(tmp_path: Path) -> None:
    path = _write(
        tmp_path,
        """
        version: 1
        host: 10.0.0.1
        port: 5020
        unit_id: 7
        poll_interval: 2.0
        timeout: 0.5
        registers:
          - name: temperature
            function: holding
            address: 100
            type: int16
            scale: 0.1
            unit: "°C"
        """,
    )
    cfg = load_config(path)
    assert isinstance(cfg, ModbusConfig)
    assert cfg.host == "10.0.0.1"
    assert cfg.port == 5020
    assert cfg.unit_id == 7
    assert cfg.poll_interval == 2.0
    assert cfg.timeout == 0.5
    assert len(cfg.registers) == 1
    reg = cfg.registers[0]
    assert reg.name == "temperature"
    assert reg.function == "holding"
    assert reg.address == 100
    assert reg.type == "int16"
    assert reg.scale == 0.1
    assert reg.unit == "°C"


def test_defaults(tmp_path: Path) -> None:
    path = _write(
        tmp_path,
        """
        version: 1
        host: 127.0.0.1
        registers:
          - name: counter
            function: input
            address: 0
            type: uint16
        """,
    )
    cfg = load_config(path)
    assert cfg.port == 502
    assert cfg.unit_id == 1
    assert cfg.poll_interval == 1.0
    assert cfg.timeout == 1.0
    assert cfg.registers[0].word_order == "ABCD"
    assert cfg.registers[0].scale == 1.0
    assert cfg.registers[0].unit == ""


def test_missing_required_field(tmp_path: Path) -> None:
    path = _write(
        tmp_path,
        """
        version: 1
        host: 127.0.0.1
        registers:
          - function: holding
            address: 0
            type: uint16
        """,
    )
    with pytest.raises(ConfigError, match="name"):
        load_config(path)


def test_unknown_function(tmp_path: Path) -> None:
    path = _write(
        tmp_path,
        """
        version: 1
        host: 127.0.0.1
        registers:
          - name: bad
            function: coil
            address: 0
            type: uint16
        """,
    )
    with pytest.raises(ConfigError, match="function"):
        load_config(path)


def test_unknown_type(tmp_path: Path) -> None:
    path = _write(
        tmp_path,
        """
        version: 1
        host: 127.0.0.1
        registers:
          - name: bad
            function: holding
            address: 0
            type: float64
        """,
    )
    with pytest.raises(ConfigError, match="type"):
        load_config(path)


def test_empty_registers(tmp_path: Path) -> None:
    path = _write(
        tmp_path,
        """
        version: 1
        host: 127.0.0.1
        registers: []
        """,
    )
    with pytest.raises(ConfigError, match="at least one"):
        load_config(path)


def test_unknown_version(tmp_path: Path) -> None:
    path = _write(
        tmp_path,
        """
        version: 99
        host: 127.0.0.1
        registers:
          - name: r
            function: holding
            address: 0
            type: uint16
        """,
    )
    with pytest.raises(ConfigError, match="version"):
        load_config(path)
