"""BaseAdapter base class"""

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
    """Adapter base class

    Base class for adapters that collect sensor/equipment data
    and send it to the Platform.

    Usage:
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
            asset_id: Asset identifier
            nats_url: NATS server URL
            collect_interval: Collection interval (seconds)
            metadata: Additional metadata
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
        """Data collection - must be implemented by subclass

        Returns:
            List of collected TagValue
        """
        pass

    async def on_start(self) -> None:
        """Called on adapter start (can be overridden)"""
        pass

    async def on_stop(self) -> None:
        """Called on adapter stop (can be overridden)"""
        pass

    async def start(self) -> None:
        """Start adapter

        Start collection loop after NATS connection.
        Handle SIGINT/SIGTERM signals.
        """
        # Setup signal handlers
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            loop.add_signal_handler(sig, lambda: asyncio.create_task(self.stop()))

        logger.info(f"Starting adapter: {self.asset_id}")
        print("=" * 40)
        print(f"  Adapter: {self.asset_id}")
        print(f"  NATS URL: {self.nats_url}")
        print(f"  Collect Interval: {self.collect_interval}s")
        print("=" * 40)

        # Connect to NATS
        await self._client.connect()

        # Start callback
        await self.on_start()

        # Start collection loop
        self._running = True
        self._task = asyncio.create_task(self._collect_loop())

        try:
            await self._task
        except asyncio.CancelledError:
            pass

    async def stop(self) -> None:
        """Stop adapter"""
        if not self._running:
            return

        logger.info(f"Stopping adapter: {self.asset_id}")
        self._running = False

        # Cancel collection task
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass

        # Stop callback
        await self.on_stop()

        # Disconnect NATS
        await self._client.disconnect()

    async def _collect_loop(self) -> None:
        """Collection loop"""
        while self._running:
            try:
                # Collect data
                values = await self.collect()

                if values:
                    # Create and publish AssetData
                    data = AssetData(
                        asset_id=self.asset_id,
                        values=values,
                        metadata=self.metadata,
                    )
                    await self._client.publish_asset_data(data)
                    logger.debug(f"Published: {len(values)} tags")

            except asyncio.CancelledError:
                raise
            except Exception as e:
                logger.error(f"Collection error: {e}")

            # Wait until next collection
            await asyncio.sleep(self.collect_interval)
