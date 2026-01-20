"""BaseAdapter base class"""

from __future__ import annotations

import asyncio
import logging
import signal
from abc import ABC, abstractmethod
from typing import Any

from .client import NATSClientWrapper
from .models import AssetData, TagValue, DeviceState
from .backoff import BackoffStrategy
from .exceptions import DeviceError, DeviceConnectionError, DeviceTimeoutError

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
        nats_max_reconnect_attempts: int = -1,
        nats_reconnect_time_wait: float = 2.0,
        nats_connect_timeout: float = 2.0,
    ):
        """
        Args:
            asset_id: Asset identifier
            nats_url: NATS server URL
            collect_interval: Collection interval (seconds)
            metadata: Additional metadata
            nats_max_reconnect_attempts: NATS max reconnection attempts (default: -1 for unlimited)
            nats_reconnect_time_wait: NATS reconnect wait time in seconds (default: 2.0)
            nats_connect_timeout: NATS connection timeout in seconds (default: 2.0)
        """
        self.asset_id = asset_id
        self.nats_url = nats_url
        self.collect_interval = collect_interval
        self.metadata = metadata

        self._client = NATSClientWrapper(
            url=nats_url,
            name=asset_id,
            max_reconnect_attempts=nats_max_reconnect_attempts,
            reconnect_time_wait=nats_reconnect_time_wait,
            connect_timeout=nats_connect_timeout,
        )
        self._running = False
        self._task: asyncio.Task[Any] | None = None
        self._device_state = DeviceState.DISCONNECTED
        self._backoff = BackoffStrategy(base=1.0, max_delay=60.0, jitter=0.1)
        self._retry_count = 0
        self._max_retries = 5
        self._device_connected = False

    @property
    def device_state(self) -> DeviceState:
        """Get current device state (read-only)

        Returns:
            Current DeviceState
        """
        return self._device_state

    async def _set_device_state(self, state: DeviceState) -> None:
        """Set device state (internal use only)

        Args:
            state: New DeviceState
        """
        self._device_state = state
        logger.debug(f"Device state changed to: {state.value}")

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

    async def connect_device(self) -> None:
        """Connect to device - override to implement device connection logic

        This hook is called when the adapter needs to establish connection
        to the physical device. Default implementation is no-op.

        Example:
            async def connect_device(self):
                self.device = await MyDevice.connect(self.device_id)
        """
        pass

    async def disconnect_device(self) -> None:
        """Disconnect from device - override to implement device disconnection logic

        This hook is called when the adapter needs to disconnect from the
        physical device. Default implementation is no-op.

        Example:
            async def disconnect_device(self):
                if self.device:
                    await self.device.close()
        """
        pass

    async def check_device_health(self) -> None:
        """Check device health - override to implement health check logic

        This hook is called periodically to verify device is operational.
        Default implementation is no-op. Raise exception if device is unhealthy.

        Example:
            async def check_device_health(self):
                if not await self.device.ping():
                    raise DeviceConnectionError("Device not responding")
        """
        pass

    async def on_device_connected(self) -> None:
        """Event hook called when device successfully connects

        Override to implement custom logic when device connects.
        Default implementation is no-op.

        Example:
            async def on_device_connected(self):
                logger.info(f"Device {self.asset_id} connected successfully")
                await self.initialize_device_settings()
        """
        pass

    async def on_device_disconnected(self, error: Exception | None = None) -> None:
        """Event hook called when device disconnects

        Override to implement custom logic when device disconnects.
        Default implementation is no-op.

        Args:
            error: Exception that caused disconnection, if any

        Example:
            async def on_device_disconnected(self, error=None):
                if error:
                    logger.error(f"Device disconnected due to: {error}")
                else:
                    logger.info(f"Device {self.asset_id} disconnected")
        """
        pass

    async def on_device_reconnected(self) -> None:
        """Event hook called when device successfully reconnects

        Override to implement custom logic when device reconnects after
        a disconnection. Default implementation is no-op.

        Example:
            async def on_device_reconnected(self):
                logger.info(f"Device {self.asset_id} reconnected")
                await self.resync_device_state()
        """
        pass

    async def _ensure_device_connected(self) -> None:
        """Ensure device is connected with retry logic

        Handles automatic connection and reconnection with exponential backoff.
        Transitions device state and calls appropriate event hooks.
        """
        if self._device_connected:
            return

        while self._retry_count < self._max_retries and self._running:
            try:
                # First attempt or reconnection
                if self._retry_count == 0:
                    await self._set_device_state(DeviceState.CONNECTING)
                else:
                    await self._set_device_state(DeviceState.RECONNECTING)

                # Call device connection hook
                await self.connect_device()

                # Mark as connected
                self._device_connected = True
                self._retry_count = 0
                await self._set_device_state(DeviceState.CONNECTED)

                # Call appropriate event hook
                if self._retry_count == 0:
                    await self.on_device_connected()
                else:
                    await self.on_device_reconnected()

                return

            except (DeviceConnectionError, DeviceTimeoutError) as e:
                self._retry_count += 1

                if self._retry_count >= self._max_retries:
                    await self._set_device_state(DeviceState.ERROR)
                    await self.on_device_disconnected(error=e)
                    raise DeviceError(
                        f"Failed to connect after {self._max_retries} attempts: {e}"
                    )

                # Calculate backoff delay
                delay = self._backoff.next_delay(self._retry_count - 1)
                logger.warning(
                    f"Device connection attempt {self._retry_count} failed: {e}. "
                    f"Retrying in {delay:.1f}s..."
                )
                await asyncio.sleep(delay)

    async def _handle_device_error(self, error: Exception) -> None:
        """Handle device errors with retry logic

        Args:
            error: Exception that occurred during device operation
        """
        if isinstance(error, (DeviceConnectionError, DeviceTimeoutError)):
            logger.warning(f"Device error: {error}")
            self._device_connected = False
            await self._set_device_state(DeviceState.DISCONNECTED)
            await self.on_device_disconnected(error=error)

            # Try to reconnect
            try:
                await self._ensure_device_connected()
            except DeviceError:
                # Max retries exceeded, error state already set
                pass
        else:
            # Non-device errors are logged but not retried
            logger.error(f"Non-device error in collection: {error}")

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

        # Disconnect from device
        if self._device_connected:
            try:
                await self.disconnect_device()
                self._device_connected = False
                await self._set_device_state(DeviceState.DISCONNECTED)
            except Exception as e:
                logger.error(f"Error disconnecting device: {e}")

        # Stop callback
        await self.on_stop()

        # Disconnect NATS
        await self._client.disconnect()

    async def _collect_loop(self) -> None:
        """Collection loop with device recovery"""
        while self._running:
            try:
                # Ensure device is connected
                await self._ensure_device_connected()

                # Check device health before collection
                await self.check_device_health()

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
            except (DeviceConnectionError, DeviceTimeoutError) as e:
                # Handle device errors with retry
                await self._handle_device_error(e)
            except Exception as e:
                # Non-device errors are logged but not retried
                logger.error(f"Collection error: {e}")

            # Wait until next collection
            await asyncio.sleep(self.collect_interval)
