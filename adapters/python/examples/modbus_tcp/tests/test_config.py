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


# --- point provisioning (ADR 0011) ---

from sdk.models import Point, PointList  # noqa: E402

from modbus_tcp.config import registers_from_points  # noqa: E402


def test_asset_id_without_registers_is_provisioned(tmp_path):
    path = tmp_path / "m.yaml"
    path.write_text("host: 10.0.0.5\nasset_id: pump-a\nnats_url: nats://core:4222\n")
    cfg = load_config(path)
    assert cfg.provisioned
    assert cfg.asset_id == "pump-a"
    assert cfg.nats_url == "nats://core:4222"


def test_registers_from_points():
    pl = PointList(
        asset_id="pump-a",
        protocol="modbus-tcp",
        version=3,
        points=[
            Point(name="temperature", value_type="NUMBER", address="0", unit="°C",
                  encoding={"function": "holding", "type": "int16", "scale": 0.1}),
            Point(name="flow", value_type="NUMBER", address="100",
                  encoding={"function": "input", "type": "float32", "word_order": "CDAB"}),
            Point(name="retired", value_type="NUMBER", address="7",
                  encoding={"type": "uint16"}, enabled=False),
        ],
    )
    regs = registers_from_points(pl)
    assert [r.name for r in regs] == ["temperature", "flow"], "a disabled point is not polled"
    assert (regs[0].address, regs[0].type, regs[0].scale, regs[0].unit) == (0, "int16", 0.1, "°C")
    assert (regs[1].function, regs[1].word_order, regs[1].scale) == ("input", "CDAB", 1.0)


@pytest.mark.parametrize(
    "point,message",
    [
        (Point(name="p", value_type="NUMBER", address="ns=2;s=T", encoding={"type": "int16"}), "not a register number"),
        (Point(name="p", value_type="NUMBER", address="1"), "encoding.type is required"),
        (Point(name="p", value_type="NUMBER", address="1", encoding={"type": "int16", "scale": "0.1"}), "must be a number"),
        (Point(name="p", value_type="NUMBER", address="1", encoding={"type": "int64"}), "not supported"),
    ],
)
def test_one_bad_point_rejects_the_list(point, message):
    good = Point(name="ok", value_type="NUMBER", address="0", encoding={"type": "uint16"})
    with pytest.raises(ConfigError, match=message):
        registers_from_points(PointList(asset_id="a", points=[good, point]))


def test_another_protocol_is_refused():
    with pytest.raises(ConfigError, match="opcua"):
        registers_from_points(PointList(asset_id="a", protocol="opcua"))
