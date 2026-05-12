"""Decoder matrix tests shared with Go via testdata/decoder_matrix.yaml.

The decoder is the heart of this example: it turns raw 16-bit register
words into a TagValue according to the configured type, word order, and
scale. We exercise every type x word-order combination so that vendor
quirks in field deployments do not regress silently.
"""

from __future__ import annotations

import math
import sys
from pathlib import Path

import pytest
import yaml

# Make the example modbus_tcp package importable when running pytest from
# the repo root (mirrors how the runnable script sets up sys.path).
_EXAMPLE_DIR = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(_EXAMPLE_DIR.parent.parent))  # adapters/python
sys.path.insert(0, str(_EXAMPLE_DIR.parent))         # adapters/python/examples

from modbus_tcp.decoder import RegisterSpec, decode_register


_MATRIX = yaml.safe_load(
    (_EXAMPLE_DIR / "testdata" / "decoder_matrix.yaml").read_text()
)["cases"]


@pytest.mark.parametrize("case", _MATRIX, ids=lambda c: c["name"])
def test_decode_matrix(case: dict) -> None:
    spec = RegisterSpec(
        name=case["name"],
        function="holding",
        address=0,
        type=case["type"],
        word_order=case.get("word_order", "ABCD"),
        scale=case.get("scale", 1.0),
        unit="",
    )

    value = decode_register(case["words"], spec)

    if "expected_approx" in case:
        assert value.number is not None
        assert math.isclose(
            value.number, case["expected_approx"], rel_tol=1e-4
        ), f"{case['name']}: {value.number} ≠ {case['expected_approx']}"
    else:
        assert value.number == case["expected"], case["name"]

    assert value.name == case["name"]
    assert value.quality == "GOOD"


def test_decode_requires_two_words_for_32bit() -> None:
    spec = RegisterSpec(
        name="bad",
        function="holding",
        address=0,
        type="uint32",
        word_order="ABCD",
    )
    with pytest.raises(ValueError, match="2 words"):
        decode_register([0x1234], spec)


def test_decode_requires_one_word_for_16bit() -> None:
    spec = RegisterSpec(
        name="bad",
        function="holding",
        address=0,
        type="uint16",
    )
    with pytest.raises(ValueError, match="1 word"):
        decode_register([0x1234, 0x5678], spec)


def test_decode_unknown_type() -> None:
    spec = RegisterSpec(
        name="bad",
        function="holding",
        address=0,
        type="float64",  # not supported in this reference adapter
    )
    with pytest.raises(ValueError, match="unsupported type"):
        decode_register([0x0000, 0x0000], spec)


def test_decode_unknown_word_order() -> None:
    spec = RegisterSpec(
        name="bad",
        function="holding",
        address=0,
        type="uint32",
        word_order="XXXX",
    )
    with pytest.raises(ValueError, match="unsupported word_order"):
        decode_register([0x0000, 0x0000], spec)
