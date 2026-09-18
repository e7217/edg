"""Point provisioning: run an adapter whose points come from master data (ADR 0011).

Core holds each asset's point list. ``run_provisioned`` fetches it at boot,
builds an adapter from it, and rebuilds when ``platform.meta.points.changed``
names the asset -- or when a periodic check finds a newer version, because
change events are best-effort and a reconnect can drop one.

Each list version gets a fresh adapter: the previous one is stopped (its
device disconnected) and the next is built from the new list. Resolving the
list before the adapter exists, rather than handing a running adapter a new
list, is what makes a change safe without coordinating with ``collect()``.
"""

from __future__ import annotations

import asyncio
import json
import logging
import signal
from typing import Callable

from .adapter import BaseAdapter
from .client import SUBJECT_POINTS_CHANGED, NATSClientWrapper
from .exceptions import SDKError
from .models import PointList, TagValue

logger = logging.getLogger(__name__)

AdapterFactory = Callable[[PointList], BaseAdapter]


class AssetNotDeclaredError(SDKError):
    """The asset has no master-data record, so it has no point list."""


UNUSABLE_LIST_INTERVAL = 10.0


class _UnusableListAdapter(BaseAdapter):
    """Stands in for an adapter the factory could not build.

    It keeps the status plane alive and reports the last version that could
    actually be run, so the drift report shows the asset as stale rather than
    converged on a list nothing can poll. Every collect fails with the reason,
    which is what puts it in the status frame's last_error.
    """

    def __init__(self, reason: Exception, **kwargs) -> None:
        super().__init__(**kwargs)
        self._reason = reason

    async def collect(self) -> list[TagValue]:
        raise self._reason


async def run_provisioned(
    asset_id: str,
    factory: AdapterFactory,
    nats_url: str = "nats://localhost:4222",
    retry_interval: float = 5.0,
    reconcile_interval: float = 300.0,
) -> None:
    """Run adapters built from the asset's point list until SIGINT/SIGTERM.

    Args:
        asset_id: The asset whose point list to follow.
        factory: Builds an adapter from a list. It is called once per version.
            If it raises, the list is unusable: nothing is collected until the
            next version arrives.
        nats_url: EDG Core's NATS URL, credentials included.
        retry_interval: Seconds between failed connects and fetches. An adapter
            that starts before core waits for it.
        reconcile_interval: Seconds between version checks without an event.
            0 disables them.

    Raises:
        AssetNotDeclaredError: The asset is not declared in master data.
    """
    client = NATSClientWrapper(url=nats_url, name=f"{asset_id}-provisioning")
    await _connect_with_retry(client, retry_interval)

    changed: asyncio.Queue[int] = asyncio.Queue(maxsize=1)

    async def on_changed(msg) -> None:
        try:
            event = json.loads(msg.data.decode())
        except ValueError:
            return
        if event.get("entity_id") != asset_id:
            return
        version = int((event.get("after") or {}).get("version", 0))
        # Latest wins: replace a value not yet read.
        if changed.full():
            changed.get_nowait()
        changed.put_nowait(version)

    # Subscribed before the first fetch, so a change landing in between is not
    # missed.
    sub = await client.nc.subscribe(SUBJECT_POINTS_CHANGED, cb=on_changed)

    stop = asyncio.Event()
    loop = asyncio.get_running_loop()

    def install_signals() -> None:
        # BaseAdapter.start() installs its own handlers; these are restored
        # between generations so a signal in the gap is not lost.
        for sig in (signal.SIGINT, signal.SIGTERM):
            loop.add_signal_handler(sig, stop.set)

    applied = [0]  # the last version an adapter could be built from
    try:
        install_signals()
        points = await _fetch_with_retry(client, asset_id, retry_interval, stop)
        while points is not None and not stop.is_set():
            next_points = await _run_generation(
                client, asset_id, nats_url, points, applied, factory, changed, stop,
                retry_interval, reconcile_interval,
            )
            install_signals()
            if next_points is None:
                return
            if next_points.version != points.version:
                logger.info(
                    "point list changed; rebuilding (v%d -> v%d)", points.version, next_points.version
                )
            points = next_points
    finally:
        await sub.unsubscribe()
        await client.disconnect()


async def _run_generation(
    client: NATSClientWrapper,
    asset_id: str,
    nats_url: str,
    points: PointList,
    applied: list[int],
    factory: AdapterFactory,
    changed: asyncio.Queue[int],
    stop: asyncio.Event,
    retry_interval: float,
    reconcile_interval: float,
) -> PointList | None:
    """Run one adapter built from ``points``. Returns the list to rebuild from,
    or None when the run should end."""
    adapter: BaseAdapter
    try:
        adapter = factory(points)
        adapter.config_version = points.version
        if points.poll_interval_ms > 0:
            adapter.collect_interval = points.poll_interval_ms / 1000.0
        applied[0] = points.version
    except Exception as e:  # the factory is user code
        logger.error(
            "point list v%d is unusable; collecting nothing until it changes: %s", points.version, e
        )
        adapter = _UnusableListAdapter(
            SDKError(f"point list v{points.version} is unusable: {e}"),
            asset_id=asset_id,
            nats_url=nats_url,
            collect_interval=UNUSABLE_LIST_INTERVAL,
            config_version=applied[0],
        )
    task: asyncio.Task | None = asyncio.create_task(adapter.start())

    async def stop_adapter() -> None:
        if task is not None:
            await adapter.stop()
            await asyncio.gather(task, return_exceptions=True)

    stop_wait = asyncio.create_task(stop.wait())
    try:
        while True:
            change_wait = asyncio.create_task(changed.get())
            waits: set[asyncio.Task] = {change_wait, stop_wait}
            if task is not None:
                waits.add(task)
            timeout = reconcile_interval if reconcile_interval > 0 else None
            done, _ = await asyncio.wait(waits, timeout=timeout, return_when=asyncio.FIRST_COMPLETED)
            if not change_wait.done():
                change_wait.cancel()

            if stop_wait in done:
                await stop_adapter()
                return None
            if task is not None and task in done:
                # The adapter ended by itself: a signal it handled, or a device
                # it gave up on. Either way this run is over.
                exc = task.exception()
                if exc is not None:
                    raise exc
                return None
            if change_wait in done:
                version = change_wait.result()
                if version and version <= points.version:
                    continue  # an older or duplicate event
                await stop_adapter()
                return await _fetch_with_retry(client, asset_id, retry_interval, stop)
            # Reconcile tick.
            try:
                current = await client.get_point_list(asset_id)
            except Exception as e:
                logger.debug("reconcile fetch failed: %s", e)
                continue
            if current is None:
                raise AssetNotDeclaredError(f"asset {asset_id!r} is no longer declared")
            if current.version != points.version:
                await stop_adapter()
                return current
    finally:
        stop_wait.cancel()


async def _connect_with_retry(client: NATSClientWrapper, retry_interval: float) -> None:
    while True:
        try:
            await client.connect()
            return
        except Exception as e:
            logger.warning("connecting to core failed; retrying in %.1fs: %s", retry_interval, e)
            await asyncio.sleep(retry_interval)


async def _fetch_with_retry(
    client: NATSClientWrapper, asset_id: str, retry_interval: float, stop: asyncio.Event
) -> PointList | None:
    while not stop.is_set():
        try:
            points = await client.get_point_list(asset_id)
        except Exception as e:
            logger.warning("fetching point list failed; retrying in %.1fs: %s", retry_interval, e)
            try:
                await asyncio.wait_for(stop.wait(), timeout=retry_interval)
            except asyncio.TimeoutError:
                pass
            continue
        if points is None:
            raise AssetNotDeclaredError(f"asset {asset_id!r} is not declared in master data")
        return points
    return None
