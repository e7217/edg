"""Adapter runtime status reporting (ADR 0008).

Mirrors adapters/go/sdk/status.go field for field. The two SDKs share a golden
JSON fixture so a field that drifts on either side fails a test rather than
silently producing frames core cannot read.
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import re
import secrets
import socket
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone
from enum import Enum
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from .client import NATSClientWrapper

logger = logging.getLogger(__name__)

SDK_VERSION = "python/0.6.0"
ADAPTER_SCHEMA_VERSION = 1

# Subjects. Mirrors internal/core/events.go and the Go SDK's subjects.go.
SUBJECT_ADAPTER_STATUS_PREFIX = "platform.adapter.status."
SUBJECT_ADAPTER_HELLO = "platform.adapter.hello"
SUBJECT_ADAPTER_PING_PREFIX = "platform.adapter.ping."

DEFAULT_HEARTBEAT_INTERVAL = 10.0

# Consecutive collect failures before the adapter calls itself degraded.
# Independent of the device link on purpose: a PLC that answers but returns
# garbage leaves the connection state "connected".
DEGRADED_AFTER_ERRORS = 3

# Bounds the wait for the goodbye frame to reach the server. Short: a shutting
# down adapter must not hang on an unreachable broker.
OFFLINE_FLUSH_TIMEOUT = 2


# Every character that cannot appear in a subject token. A dot would silently
# reshape platform.adapter.status.<id> into extra tokens.
_ADAPTER_ID_INVALID = re.compile(r"[^A-Za-z0-9_:\-]")


def sanitize_adapter_id(adapter_id: str) -> str:
    """Make an id usable as a subject token, deterministically so a restart
    maps to the same id."""
    out = _ADAPTER_ID_INVALID.sub("-", adapter_id)
    if not out:
        return "adapter"
    out = out[:64]
    if not out[0].isalnum():
        out = ("a" + out)[:64]
    return out


class RunState(str, Enum):
    """Adapter process lifecycle, distinct from the device link."""

    STARTING = "starting"
    RUNNING = "running"
    DEGRADED = "degraded"
    STOPPING = "stopping"
    STOPPED = "stopped"


class AdapterPhase(str, Enum):
    """Why a frame was sent. Lets core tell a clean shutdown from silence."""

    ONLINE = "online"
    HEARTBEAT = "heartbeat"
    ANNOUNCE = "announce"
    PROBE = "probe"
    OFFLINE = "offline"


@dataclass
class AdapterCounters:
    """Monotonic totals. Core differences consecutive observations."""

    published_total: int = 0
    collect_errors_total: int = 0
    device_errors_total: int = 0
    device_reconnects_total: int = 0
    nats_reconnects_total: int = 0
    consecutive_collect_errors: int = 0

    def to_dict(self) -> dict:
        return {
            "published_total": self.published_total,
            "collect_errors_total": self.collect_errors_total,
            "device_errors_total": self.device_errors_total,
            "device_reconnects_total": self.device_reconnects_total,
            "nats_reconnects_total": self.nats_reconnects_total,
            "consecutive_collect_errors": self.consecutive_collect_errors,
        }


@dataclass
class StatusReporter:
    """Publishes adapter runtime status on a background asyncio task."""

    client: "NATSClientWrapper"
    asset_id: str
    adapter_id: str = ""
    adapter_version: str = ""
    heartbeat_interval: float = DEFAULT_HEARTBEAT_INTERVAL
    report_host: bool = False

    counters: AdapterCounters = field(default_factory=AdapterCounters)
    _seq: int = 0
    _instance_id: str = ""
    _started_at: float = 0.0
    _device_state: str = "disconnected"
    _last_error: str = ""
    _last_error_at: str | None = None
    _task: asyncio.Task | None = None
    _subs: list = field(default_factory=list)
    _dirty: asyncio.Event | None = None
    _running: bool = False

    def __post_init__(self) -> None:
        if not self.adapter_id:
            # One adapter per asset needs no new configuration.
            self.adapter_id = self.asset_id
        # adapter_id is the last token of the status subject, so it must be a
        # single NATS token. asset_id has no such constraint and the default
        # falls back to it: an asset named "line3.press" would produce frames
        # core drops, with no symptom beyond the adapter never appearing.
        sanitized = sanitize_adapter_id(self.adapter_id)
        if sanitized != self.adapter_id:
            logger.warning(
                "adapter_id %r is not a valid NATS subject token; reporting as %r. "
                "Set adapter_id explicitly to control it.",
                self.adapter_id,
                sanitized,
            )
            self.adapter_id = sanitized
        if self.heartbeat_interval <= 0:
            self.heartbeat_interval = DEFAULT_HEARTBEAT_INTERVAL
        # Identifies this process run so core can detect restarts and two
        # adapters sharing an id.
        self._instance_id = secrets.token_hex(8)
        self._started_at = time.time()

    async def start(self) -> None:
        """Publish the online frame, subscribe to hello/ping and begin
        heartbeating."""
        self._dirty = asyncio.Event()
        self._running = True
        await self._subscribe_control()
        await self.publish(AdapterPhase.ONLINE)
        self._task = asyncio.create_task(self._loop())

    async def stop(self) -> None:
        """Send the goodbye frame and halt.

        Must be awaited before the NATS connection closes, which is why
        BaseAdapter.stop calls it ahead of client.disconnect().
        """
        self._running = False
        if self._dirty is not None:
            self._dirty.set()
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
            self._task = None

        # Stop answering before saying goodbye: a probe answered after the
        # offline frame would resurrect a stopped adapter in core's registry.
        for sub in self._subs:
            try:
                await sub.unsubscribe()
            except Exception as e:  # pragma: no cover - defensive
                logger.debug(f"Unsubscribe adapter control failed: {e}")
        self._subs = []

        await self.publish(AdapterPhase.OFFLINE)

        # Publishing is buffered and disconnect() drains asynchronously, so
        # without an explicit flush the goodbye frame is lost whenever the
        # process exits promptly. Core would then report the adapter stale
        # minutes later instead of knowing at once that it stopped cleanly.
        nc = self.client.nc
        if nc is not None:
            try:
                await nc.flush(timeout=OFFLINE_FLUSH_TIMEOUT)
            except Exception as e:
                logger.debug(f"Flush adapter offline frame failed: {e}")

    async def _subscribe_control(self) -> None:
        nc = self.client.nc
        if nc is None:
            return
        try:
            # hello: core restarted and wants everyone to re-announce.
            self._subs.append(await nc.subscribe(
                SUBJECT_ADAPTER_HELLO,
                cb=lambda _msg: asyncio.create_task(self.publish(AdapterPhase.ANNOUNCE)),
            ))
            # ping: core missed a heartbeat and checks before declaring us dead.
            self._subs.append(await nc.subscribe(
                SUBJECT_ADAPTER_PING_PREFIX + self.adapter_id,
                cb=self._handle_ping,
            ))
        except Exception as e:  # pragma: no cover - defensive
            logger.warning(f"Adapter status control subscriptions failed: {e}")

    async def _handle_ping(self, msg) -> None:
        if msg.reply:
            await self.client.nc.publish(msg.reply, b'{"ok":true}')

    async def _loop(self) -> None:
        while self._running:
            try:
                # Wake on either the heartbeat tick or an edge-triggered
                # transition, so a state change is reported at once instead of
                # up to a whole interval later.
                await asyncio.wait_for(self._dirty.wait(), timeout=self.heartbeat_interval)
                self._dirty.clear()
            except asyncio.TimeoutError:
                pass
            except asyncio.CancelledError:
                return
            if not self._running:
                return
            await self.publish(AdapterPhase.HEARTBEAT)

    def mark_dirty(self) -> None:
        """Request an out-of-band frame. Safe to call from the collect loop."""
        if self._dirty is not None:
            self._dirty.set()

    def set_device_state(self, state: str) -> None:
        changed = self._device_state != state
        self._device_state = state
        if not changed:
            return
        if state in ("error", "disconnected"):
            self.counters.device_errors_total += 1
        if state == "reconnecting":
            self.counters.device_reconnects_total += 1
        self.mark_dirty()

    def inc_published(self) -> None:
        self.counters.published_total += 1
        self.counters.consecutive_collect_errors = 0

    def inc_collect_error(self, error: Exception) -> None:
        self.counters.collect_errors_total += 1
        self.counters.consecutive_collect_errors += 1
        self._last_error = str(error)
        self._last_error_at = _now_iso()
        # Report the moment the adapter becomes degraded; that transition is
        # the whole point of the axis.
        if self.counters.consecutive_collect_errors == DEGRADED_AFTER_ERRORS:
            self.mark_dirty()

    def run_state(self) -> RunState:
        if self.counters.consecutive_collect_errors >= DEGRADED_AFTER_ERRORS:
            return RunState.DEGRADED
        return RunState.RUNNING

    def frame(self, phase: AdapterPhase) -> dict:
        """Build a status frame. Field names and types mirror the Go SDK."""
        self._seq += 1
        now = time.time()
        run_state = RunState.STOPPED if phase == AdapterPhase.OFFLINE else self.run_state()

        asset: dict = {
            "asset_id": self.asset_id,
            "device_state": self._device_state,
            "published_total": self.counters.published_total,
            "collect_errors_total": self.counters.collect_errors_total,
        }
        if self._last_error:
            asset["last_error"] = self._last_error
            asset["last_error_at"] = self._last_error_at

        frame: dict = {
            "schema_version": ADAPTER_SCHEMA_VERSION,
            "adapter_id": self.adapter_id,
            "instance_id": self._instance_id,
            "seq": self._seq,
            "phase": phase.value,
            "run_state": run_state.value,
            "device_state": self._device_state,
            "heartbeat_interval_s": int(self.heartbeat_interval),
            "uptime_s": int(now - self._started_at),
            "started_at": _iso(self._started_at),
            # Diagnostic only: core judges liveness by its own receive time, so
            # a wrong clock here cannot make a live adapter look dead.
            "sent_at": _now_iso(),
            "sdk": SDK_VERSION,
            "capabilities": ["ping"],
            "config_version": 0,
            "assets": [asset],
            "device_counts": {self._device_state: 1},
            "counters": self.counters.to_dict(),
        }
        if self.adapter_version:
            frame["adapter_version"] = self.adapter_version
        if self.report_host:
            frame["host"] = socket.gethostname()
            frame["pid"] = os.getpid()
        return frame

    async def publish(self, phase: AdapterPhase) -> None:
        nc = self.client.nc
        if nc is None:
            return
        try:
            payload = json.dumps(self.frame(phase)).encode()
            await nc.publish(SUBJECT_ADAPTER_STATUS_PREFIX + self.adapter_id, payload)
        except Exception as e:
            # Status is best-effort by design: failing to report must never
            # take down an adapter that is otherwise collecting fine.
            logger.debug(f"Publish adapter status failed: {e}")


def _iso(epoch: float) -> str:
    return datetime.fromtimestamp(epoch, tz=timezone.utc).isoformat().replace("+00:00", "Z")


def _now_iso() -> str:
    return _iso(time.time())
