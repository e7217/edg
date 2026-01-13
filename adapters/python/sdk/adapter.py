"""BaseAdapter 베이스 클래스"""

from __future__ import annotations

import asyncio
import logging
import signal
from abc import ABC, abstractmethod
from typing import Any

from .client import NATSClientWrapper
from .models import AssetData, TagValue

logger = logging.getLogger(__name__)


class BaseAdapter(ABC):
    """Adapter 베이스 클래스

    센서/장비 데이터를 수집하여 Platform으로 전송하는
    어댑터의 기본 클래스입니다.

    사용법:
        class MySensor(BaseAdapter):
            async def collect(self) -> list[TagValue]:
                return [TagValue(name="temp", number=25.5, unit="°C")]

        adapter = MySensor(asset_id="sensor-001")
        asyncio.run(adapter.start())
    """

    def __init__(
        self,
        asset_id: str,
        nats_url: str = "nats://localhost:4222",
        collect_interval: float = 1.0,
        metadata: dict[str, str] | None = None,
    ):
        """
        Args:
            asset_id: Asset 식별자
            nats_url: NATS 서버 URL
            collect_interval: 수집 주기 (초)
            metadata: 추가 메타데이터
        """
        self.asset_id = asset_id
        self.nats_url = nats_url
        self.collect_interval = collect_interval
        self.metadata = metadata

        self._client = NATSClientWrapper(url=nats_url, name=asset_id)
        self._running = False
        self._task: asyncio.Task[Any] | None = None

    @abstractmethod
    async def collect(self) -> list[TagValue]:
        """데이터 수집 - 하위 클래스에서 구현 필수

        Returns:
            수집된 TagValue 리스트
        """
        pass

    async def on_start(self) -> None:
        """어댑터 시작 시 호출 (오버라이드 가능)"""
        pass

    async def on_stop(self) -> None:
        """어댑터 종료 시 호출 (오버라이드 가능)"""
        pass

    async def start(self) -> None:
        """어댑터 시작

        NATS 연결 후 수집 루프를 시작합니다.
        SIGINT/SIGTERM 시그널을 처리합니다.
        """
        # 시그널 핸들러 설정
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            loop.add_signal_handler(sig, lambda: asyncio.create_task(self.stop()))

        logger.info(f"어댑터 시작: {self.asset_id}")
        print("=" * 40)
        print(f"  Adapter: {self.asset_id}")
        print(f"  NATS URL: {self.nats_url}")
        print(f"  Collect Interval: {self.collect_interval}s")
        print("=" * 40)

        # NATS 연결
        await self._client.connect()

        # 시작 콜백
        await self.on_start()

        # 수집 루프 시작
        self._running = True
        self._task = asyncio.create_task(self._collect_loop())

        try:
            await self._task
        except asyncio.CancelledError:
            pass

    async def stop(self) -> None:
        """어댑터 종료"""
        if not self._running:
            return

        logger.info(f"어댑터 종료: {self.asset_id}")
        self._running = False

        # 수집 태스크 취소
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass

        # 종료 콜백
        await self.on_stop()

        # NATS 연결 종료
        await self._client.disconnect()

    async def _collect_loop(self) -> None:
        """수집 루프"""
        while self._running:
            try:
                # 데이터 수집
                values = await self.collect()

                if values:
                    # AssetData 생성 및 발행
                    data = AssetData(
                        asset_id=self.asset_id,
                        values=values,
                        metadata=self.metadata,
                    )
                    await self._client.publish_asset_data(data)
                    logger.debug(f"발행 완료: {len(values)} tags")

            except asyncio.CancelledError:
                raise
            except Exception as e:
                logger.error(f"수집 에러: {e}")

            # 다음 수집까지 대기
            await asyncio.sleep(self.collect_interval)
