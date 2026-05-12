"""Pure register decoder.

Splitting this out from the adapter lets us cover every type x word_order
combination with a fast unit-test matrix that is shared with the Go
implementation via ``testdata/decoder_matrix.yaml``.

Word-order naming follows the convention vendors print in PLC manuals:
``ABCD`` (normal big-endian), ``CDAB`` (word swap), ``BADC`` (byte swap
inside each word), ``DCBA`` (both swapped).
"""

from __future__ import annotations

import struct
from dataclasses import dataclass

from sdk import TagValue


_SCALAR_16 = {"uint16", "int16"}
_SCALAR_32 = {"uint32", "int32", "float32"}
_WORD_ORDERS = ("ABCD", "CDAB", "BADC", "DCBA")


@dataclass
class RegisterSpec:
    """One row in the mapping YAML."""

    name: str
    function: str           # "holding" | "input"
    address: int
    type: str               # "uint16" | "int16" | "uint32" | "int32" | "float32"
    word_order: str = "ABCD"
    scale: float = 1.0
    unit: str = ""

    @property
    def word_count(self) -> int:
        if self.type in _SCALAR_16:
            return 1
        if self.type in _SCALAR_32:
            return 2
        raise ValueError(f"unsupported type: {self.type}")


def decode_register(words: list[int], spec: RegisterSpec) -> TagValue:
    """Decode raw 16-bit register words into a TagValue.

    Returns a TagValue with quality=GOOD. The adapter is responsible for
    downgrading quality on read errors before publish.
    """
    if spec.type in _SCALAR_16:
        if len(words) != 1:
            raise ValueError(f"{spec.type} requires 1 word, got {len(words)}")
        raw = words[0] & 0xFFFF
        if spec.type == "int16":
            raw = _sign_extend(raw, 16)
        value = float(raw)

    elif spec.type in _SCALAR_32:
        if len(words) != 2:
            raise ValueError(f"{spec.type} requires 2 words, got {len(words)}")
        if spec.word_order not in _WORD_ORDERS:
            raise ValueError(
                f"unsupported word_order: {spec.word_order} "
                f"(expected one of {_WORD_ORDERS})"
            )
        raw_bytes = _to_big_endian_bytes(words, spec.word_order)
        if spec.type == "uint32":
            value = float(int.from_bytes(raw_bytes, "big", signed=False))
        elif spec.type == "int32":
            value = float(int.from_bytes(raw_bytes, "big", signed=True))
        else:  # float32
            value = struct.unpack(">f", raw_bytes)[0]

    else:
        raise ValueError(f"unsupported type: {spec.type}")

    return TagValue(
        name=spec.name,
        quality="GOOD",
        number=value * spec.scale,
        unit=spec.unit,
    )


def _sign_extend(value: int, bits: int) -> int:
    sign_bit = 1 << (bits - 1)
    return (value & (sign_bit - 1)) - (value & sign_bit)


def _to_big_endian_bytes(words: list[int], word_order: str) -> bytes:
    """Reassemble 4 bytes [A B C D] in canonical big-endian order.

    Given two raw register words exactly as returned by the device, swap
    bytes/words so the result represents the logical big-endian value.
    """
    hi, lo = words[0] & 0xFFFF, words[1] & 0xFFFF
    a, b = (hi >> 8) & 0xFF, hi & 0xFF
    c, d = (lo >> 8) & 0xFF, lo & 0xFF

    layout = {
        "ABCD": (a, b, c, d),
        "CDAB": (c, d, a, b),
        "BADC": (b, a, d, c),
        "DCBA": (d, c, b, a),
    }[word_order]
    return bytes(layout)
