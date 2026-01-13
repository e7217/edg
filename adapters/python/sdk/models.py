"""Data models - Compatible with Go types"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Any


@dataclass
class TagValue:
    """Individual tag value - Compatible with Go's TagValue

    Attributes:
        name: Tag name (required)
        quality: Data quality (GOOD, BAD, UNCERTAIN)
        number: Numeric value (float)
        text: String value
        flag: Boolean value
        unit: Unit (e.g., °C, %, mm/s)
    """

    name: str
    quality: str = "GOOD"
    number: float | None = None
    text: str | None = None
    flag: bool | None = None
    unit: str = ""

    def to_dict(self) -> dict[str, Any]:
        """Convert to dictionary for JSON serialization - exclude None values"""
        result: dict[str, Any] = {"name": self.name, "quality": self.quality}

        if self.number is not None:
            result["number"] = self.number
        if self.text is not None:
            result["text"] = self.text
        if self.flag is not None:
            result["flag"] = self.flag
        if self.unit:
            result["unit"] = self.unit

        return result


@dataclass
class AssetData:
    """Asset data - Compatible with Go's AssetData

    Attributes:
        asset_id: Asset identifier
        timestamp: Timestamp (milliseconds, epoch)
        values: List of tag values
        metadata: Additional metadata
    """

    asset_id: str
    values: list[TagValue]
    timestamp: int = field(default_factory=lambda: int(time.time() * 1000))
    metadata: dict[str, str] | None = None

    def to_dict(self) -> dict[str, Any]:
        """Convert to dictionary for JSON serialization"""
        result: dict[str, Any] = {
            "asset_id": self.asset_id,
            "timestamp": self.timestamp,
            "values": [v.to_dict() for v in self.values],
        }

        if self.metadata:
            result["metadata"] = self.metadata

        return result
