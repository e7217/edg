"""데이터 모델 - Go 타입과 호환"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Any


@dataclass
class TagValue:
    """개별 태그 값 - Go의 TagValue와 호환

    Attributes:
        name: 태그 이름 (필수)
        quality: 데이터 품질 (GOOD, BAD, UNCERTAIN)
        number: 숫자 값 (float)
        text: 문자열 값
        flag: 불리언 값
        unit: 단위 (예: °C, %, mm/s)
    """

    name: str
    quality: str = "GOOD"
    number: float | None = None
    text: str | None = None
    flag: bool | None = None
    unit: str = ""

    def to_dict(self) -> dict[str, Any]:
        """JSON 직렬화용 딕셔너리 변환 - None 값 제외"""
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
    """Asset 데이터 - Go의 AssetData와 호환

    Attributes:
        asset_id: Asset 식별자
        timestamp: 타임스탬프 (밀리초, epoch)
        values: 태그 값 리스트
        metadata: 추가 메타데이터
    """

    asset_id: str
    values: list[TagValue]
    timestamp: int = field(default_factory=lambda: int(time.time() * 1000))
    metadata: dict[str, str] | None = None

    def to_dict(self) -> dict[str, Any]:
        """JSON 직렬화용 딕셔너리 변환"""
        result: dict[str, Any] = {
            "asset_id": self.asset_id,
            "timestamp": self.timestamp,
            "values": [v.to_dict() for v in self.values],
        }

        if self.metadata:
            result["metadata"] = self.metadata

        return result
