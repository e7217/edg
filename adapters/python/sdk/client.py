"""NATS client wrapper"""

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

# NATS topics
TOPIC_ASSET_DATA = "platform.data.asset"


class NATSClientWrapper:
    """NATS client wrapper

    Manages connections and data publishing.
    """

    def __init__(
        self,
        url: str = "nats://localhost:4222",
        name: str | None = None,
    ):
        """
        Args:
            url: NATS server URL
            name: Client name (for logging)
        """
        self.url = url
        self.name = name
        self._nc: NATSClient | None = None

    @property
    def is_connected(self) -> bool:
        """Check connection status"""
        return self._nc is not None and self._nc.is_connected

    async def connect(self) -> None:
        """Connect to NATS server"""
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
            logger.info(f"NATS connected: {self.url}")
        except Exception as e:
            raise ConnectionError(f"NATS connection failed: {e}") from e

    async def disconnect(self) -> None:
        """Disconnect from NATS"""
        if self._nc is not None:
            await self._nc.drain()
            self._nc = None
            logger.info("NATS disconnected")

    async def publish_asset_data(self, data: AssetData) -> None:
        """Publish asset data

        Args:
            data: AssetData to publish

        Raises:
            PublishError: When publish fails
        """
        if not self.is_connected:
            raise PublishError("NATS not connected")

        try:
            payload = json.dumps(data.to_dict()).encode()
            await self._nc.publish(TOPIC_ASSET_DATA, payload)
            logger.debug(f"Published data: {data.asset_id}, {len(data.values)} tags")
        except Exception as e:
            raise PublishError(f"Data publish failed: {e}") from e

    async def _error_callback(self, e: Exception) -> None:
        """Error callback"""
        logger.error(f"NATS error: {e}")

    async def _disconnected_callback(self) -> None:
        """Disconnected callback"""
        logger.warning("NATS disconnected")

    async def _reconnected_callback(self) -> None:
        """Reconnected callback"""
        logger.info("NATS reconnected")
