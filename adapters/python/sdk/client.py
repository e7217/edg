"""NATS 클라이언트 래퍼"""

from __future__ import annotations

import json
import logging
from typing import TYPE_CHECKING

import nats

from .exceptions import ConnectionError, PublishError
from .models import AssetData

if TYPE_CHECKING:
    from nats.aio.client import Client as NATSClient

logger = logging.getLogger(__name__)

# NATS 토픽
TOPIC_ASSET_DATA = "platform.data.asset"


class NATSClientWrapper:
    """NATS 클라이언트 래퍼

    연결 관리 및 데이터 발행을 담당합니다.
    """

    def __init__(
        self,
        url: str = "nats://localhost:4222",
        name: str | None = None,
    ):
        """
        Args:
            url: NATS 서버 URL
            name: 클라이언트 이름 (로깅용)
        """
        self.url = url
        self.name = name
        self._nc: NATSClient | None = None

    @property
    def is_connected(self) -> bool:
        """연결 상태 확인"""
        return self._nc is not None and self._nc.is_connected

    async def connect(self) -> None:
        """NATS 서버 연결"""
        if self.is_connected:
            return

        try:
            self._nc = await nats.connect(
                self.url,
                name=self.name,
                error_cb=self._error_callback,
                disconnected_cb=self._disconnected_callback,
                reconnected_cb=self._reconnected_callback,
            )
            logger.info(f"NATS 연결 성공: {self.url}")
        except Exception as e:
            raise ConnectionError(f"NATS 연결 실패: {e}") from e

    async def disconnect(self) -> None:
        """NATS 연결 종료"""
        if self._nc is not None:
            await self._nc.drain()
            self._nc = None
            logger.info("NATS 연결 종료")

    async def publish_asset_data(self, data: AssetData) -> None:
        """Asset 데이터 발행

        Args:
            data: 발행할 AssetData

        Raises:
            PublishError: 발행 실패 시
        """
        if not self.is_connected:
            raise PublishError("NATS 연결되지 않음")

        try:
            payload = json.dumps(data.to_dict()).encode()
            await self._nc.publish(TOPIC_ASSET_DATA, payload)
            logger.debug(f"데이터 발행: {data.asset_id}, {len(data.values)} tags")
        except Exception as e:
            raise PublishError(f"데이터 발행 실패: {e}") from e

    async def _error_callback(self, e: Exception) -> None:
        """에러 콜백"""
        logger.error(f"NATS 에러: {e}")

    async def _disconnected_callback(self) -> None:
        """연결 끊김 콜백"""
        logger.warning("NATS 연결 끊김")

    async def _reconnected_callback(self) -> None:
        """재연결 콜백"""
        logger.info("NATS 재연결 성공")
