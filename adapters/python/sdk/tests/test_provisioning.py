"""Tests for run_provisioned (ADR 0011).

The NATS client and the adapters are fakes: what is under test is the
generation loop -- build, rebuild on change, reconcile a missed change, survive
an unusable list -- not the transport.
"""

from __future__ import annotations

import asyncio
import json
import unittest
from unittest import mock

from .. import provisioning
from ..models import Point, PointList
from ..provisioning import AssetNotDeclaredError, run_provisioned


def plist(version: int, *names: str, poll_ms: int = 0) -> PointList:
    return PointList(
        asset_id="pump-a",
        protocol="test",
        version=version,
        poll_interval_ms=poll_ms,
        points=[Point(name=n, value_type="NUMBER", address=n) for n in names],
    )


class FakeMsg:
    def __init__(self, payload: dict):
        self.data = json.dumps(payload).encode()


class FakeSub:
    async def unsubscribe(self) -> None:
        pass


class FakeNC:
    def __init__(self) -> None:
        self.cb = None

    async def subscribe(self, subject, cb):
        self.cb = cb
        return FakeSub()


class FakeClient:
    """Stands in for NATSClientWrapper, serving a mutable point list."""

    current: PointList | None = None
    instance: "FakeClient | None" = None

    def __init__(self, url: str, name: str) -> None:
        self.nc = FakeNC()
        FakeClient.instance = self

    async def connect(self) -> None:
        pass

    async def disconnect(self) -> None:
        pass

    async def get_point_list(self, asset_id: str) -> PointList | None:
        return FakeClient.current

    async def announce(self, version: int, asset_id: str = "pump-a") -> None:
        await self.nc.cb(FakeMsg({"entity_id": asset_id, "after": {"version": version}}))


class FakeAdapter:
    """Duck-types the parts of BaseAdapter run_provisioned uses."""

    def __init__(self, points: PointList) -> None:
        self.points = points
        self.config_version = 0
        self.collect_interval = 1.0
        self.started = asyncio.Event()
        self.stopped = False
        self._halt = asyncio.Event()

    async def start(self) -> None:
        self.started.set()
        await self._halt.wait()

    async def stop(self) -> None:
        self.stopped = True
        self._halt.set()


class Recorder:
    def __init__(self, fail_versions: set[int] | None = None) -> None:
        self.built: list[FakeAdapter] = []
        self.fail_versions = fail_versions or set()

    def factory(self, points: PointList) -> FakeAdapter:
        if points.version in self.fail_versions:
            self.built.append(None)  # type: ignore[arg-type]
            raise ValueError(f"v{points.version} is not a valid register map")
        adapter = FakeAdapter(points)
        self.built.append(adapter)
        return adapter

    def versions(self) -> list[int | None]:
        return [a.points.version if a else None for a in self.built]


async def wait_until(cond, timeout: float = 2.0) -> None:
    async def poll():
        while not cond():
            await asyncio.sleep(0.01)

    await asyncio.wait_for(poll(), timeout)


class TestRunProvisioned(unittest.IsolatedAsyncioTestCase):
    def setUp(self) -> None:
        patcher = mock.patch.object(provisioning, "NATSClientWrapper", FakeClient)
        patcher.start()
        self.addCleanup(patcher.stop)
        FakeClient.current = plist(1, "temperature", poll_ms=250)

    async def start(self, rec: Recorder, reconcile: float = 0) -> asyncio.Task:
        task = asyncio.create_task(
            run_provisioned("pump-a", rec.factory, retry_interval=0.01, reconcile_interval=reconcile)
        )
        self.addAsyncCleanup(self._cancel, task)
        return task

    async def _cancel(self, task: asyncio.Task) -> None:
        task.cancel()
        await asyncio.gather(task, return_exceptions=True)

    async def test_builds_from_the_declared_list(self):
        rec = Recorder()
        await self.start(rec)
        await wait_until(lambda: rec.built and rec.built[0].started.is_set())
        adapter = rec.built[0]
        self.assertEqual(adapter.config_version, 1, "the applied version is reported")
        self.assertEqual(adapter.collect_interval, 0.25, "poll_interval_ms drives the interval")

    async def test_rebuilds_on_a_change_event(self):
        rec = Recorder()
        await self.start(rec)
        await wait_until(lambda: len(rec.built) == 1 and rec.built[0].started.is_set())

        FakeClient.current = plist(2, "temperature", "pressure")
        await FakeClient.instance.announce(2)
        await wait_until(lambda: len(rec.built) == 2)
        self.assertTrue(rec.built[0].stopped, "the previous generation is stopped first")
        self.assertEqual(rec.versions(), [1, 2])

    async def test_ignores_stale_and_foreign_events(self):
        FakeClient.current = plist(5, "temperature")
        rec = Recorder()
        await self.start(rec)
        await wait_until(lambda: len(rec.built) == 1)

        await FakeClient.instance.announce(4)
        await FakeClient.instance.announce(9, asset_id="pump-b")
        await asyncio.sleep(0.1)
        self.assertEqual(rec.versions(), [5])

    async def test_reconciles_a_missed_event(self):
        rec = Recorder()
        await self.start(rec, reconcile=0.05)
        await wait_until(lambda: len(rec.built) == 1)

        FakeClient.current = plist(3, "flow")  # no event
        await wait_until(lambda: len(rec.built) == 2)
        self.assertEqual(rec.versions(), [1, 3])

    async def test_undeclared_asset_is_an_error(self):
        FakeClient.current = None
        with self.assertRaises(AssetNotDeclaredError):
            await run_provisioned("pump-a", Recorder().factory, retry_interval=0.01)

    async def test_survives_an_unusable_list(self):
        rec = Recorder(fail_versions={2})
        idle: list = []

        async def fake_start(self):  # no NATS in these tests
            self._halt = asyncio.Event()
            idle.append(self)
            await self._halt.wait()

        async def fake_stop(self):
            self._halt.set()

        with mock.patch.object(provisioning._UnusableListAdapter, "start", fake_start), \
                mock.patch.object(provisioning._UnusableListAdapter, "stop", fake_stop):
            await self.start(rec)
            await wait_until(lambda: len(rec.built) == 1)

            FakeClient.current = plist(2, "bad")
            await FakeClient.instance.announce(2)
            await wait_until(lambda: len(idle) == 1)
            self.assertEqual(idle[0].config_version, 1,
                             "a stuck adapter reports the last usable version, not the declared one")

            FakeClient.current = plist(3, "good")
            await FakeClient.instance.announce(3)
            await wait_until(lambda: len(rec.built) == 3)
            self.assertEqual(rec.versions(), [1, None, 3])


class TestPointList(unittest.TestCase):
    def test_from_dict_keeps_encoding_types_and_defaults_enabled(self):
        pl = PointList.from_dict(
            {
                "asset_id": "pump-a",
                "protocol": "modbus-tcp",
                "version": 4,
                "poll_interval_ms": 500,
                "points": [
                    {"name": "t", "value_type": "NUMBER", "address": "0", "encoding": {"scale": 0.1}},
                    {"name": "d", "value_type": "NUMBER", "address": "1", "enabled": False},
                ],
            }
        )
        self.assertEqual(pl.version, 4)
        self.assertEqual(pl.points[0].encoding["scale"], 0.1)
        self.assertTrue(pl.points[0].enabled, "an absent enabled key means enabled")
        self.assertEqual([p.name for p in pl.enabled_points()], ["t"])

    def test_empty_list(self):
        pl = PointList.from_dict({"asset_id": "pump-a", "version": 0, "points": None})
        self.assertEqual(pl.points, [])
