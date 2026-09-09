"""Adapter runtime status reporting (ADR 0008).

The golden fixture in this directory is a byte-for-byte copy of the Go SDK's
adapters/go/sdk/testdata/adapter_status.json. Both suites parse it, so a field
that drifts on either side fails a test instead of producing frames core
silently cannot read.
"""

import json
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

import pytest

from ..status import (
    ADAPTER_SCHEMA_VERSION,
    DEGRADED_AFTER_ERRORS,
    SUBJECT_ADAPTER_STATUS_PREFIX,
    AdapterPhase,
    RunState,
    StatusReporter,
)

GOLDEN = Path(__file__).parent / "adapter_status.json"


def make_reporter(**kwargs) -> StatusReporter:
    client = MagicMock()
    client.nc = AsyncMock()
    return StatusReporter(client=client, asset_id="press-01", **kwargs)


class TestGoldenCompatibility:
    """The cross-SDK contract."""

    def test_golden_parses(self):
        frame = json.loads(GOLDEN.read_text())
        assert frame["schema_version"] == ADAPTER_SCHEMA_VERSION
        assert frame["run_state"] == RunState.RUNNING.value
        assert frame["phase"] == AdapterPhase.HEARTBEAT.value
        assert frame["device_state"] == "connected"

    def test_python_frame_has_every_golden_key(self):
        """A field the Go SDK emits and Python omits would be invisible to
        anyone reading only Python frames."""
        golden = json.loads(GOLDEN.read_text())
        reporter = make_reporter(adapter_id="modbus-line3", adapter_version="modbus-tcp/1.2.0")
        reporter.set_device_state("connected")
        frame = reporter.frame(AdapterPhase.HEARTBEAT)

        # host/pid are opt-in on both sides and absent from the golden fixture.
        missing = set(golden) - set(frame)
        assert not missing, f"Python frame is missing Go fields: {missing}"

    def test_counter_keys_match(self):
        golden = json.loads(GOLDEN.read_text())["counters"]
        frame = make_reporter().frame(AdapterPhase.HEARTBEAT)
        assert set(frame["counters"]) == set(golden)

    def test_asset_keys_match(self):
        golden_asset = json.loads(GOLDEN.read_text())["assets"][0]
        reporter = make_reporter()
        reporter.inc_collect_error(RuntimeError("read timeout"))
        asset = reporter.frame(AdapterPhase.HEARTBEAT)["assets"][0]
        assert set(golden_asset) == set(asset)


class TestIdentity:
    def test_adapter_id_falls_back_to_asset_id(self):
        assert make_reporter().adapter_id == "press-01"

    def test_explicit_adapter_id_wins(self):
        assert make_reporter(adapter_id="modbus-line3").adapter_id == "modbus-line3"

    def test_instance_id_is_unique_per_reporter(self):
        a, b = make_reporter(), make_reporter()
        assert a._instance_id != b._instance_id
        assert len(a._instance_id) == 16

    def test_seq_increments(self):
        r = make_reporter()
        assert r.frame(AdapterPhase.HEARTBEAT)["seq"] == 1
        assert r.frame(AdapterPhase.HEARTBEAT)["seq"] == 2


class TestRunStateDerivation:
    def test_running_by_default(self):
        assert make_reporter().run_state() == RunState.RUNNING

    def test_degraded_after_consecutive_errors(self):
        """The device link stays connected; only the adapter is failing."""
        r = make_reporter()
        r.set_device_state("connected")
        for _ in range(DEGRADED_AFTER_ERRORS):
            r.inc_collect_error(RuntimeError("garbage register value"))

        frame = r.frame(AdapterPhase.HEARTBEAT)
        assert frame["run_state"] == RunState.DEGRADED.value
        assert frame["device_state"] == "connected"

    def test_success_resets_the_streak(self):
        r = make_reporter()
        for _ in range(DEGRADED_AFTER_ERRORS):
            r.inc_collect_error(RuntimeError("boom"))
        assert r.run_state() == RunState.DEGRADED

        r.inc_published()
        assert r.run_state() == RunState.RUNNING

    def test_offline_phase_reports_stopped(self):
        assert make_reporter().frame(AdapterPhase.OFFLINE)["run_state"] == RunState.STOPPED.value


class TestFrameContents:
    def test_host_is_opt_in(self):
        frame = make_reporter().frame(AdapterPhase.HEARTBEAT)
        assert "host" not in frame and "pid" not in frame

    def test_host_included_when_requested(self):
        frame = make_reporter(report_host=True).frame(AdapterPhase.HEARTBEAT)
        assert frame["host"] and frame["pid"] > 0

    def test_config_version_always_present(self):
        """Reserved for the declaration layer; omitting it would make
        'not supported' indistinguishable from 'version 0'."""
        assert make_reporter().frame(AdapterPhase.HEARTBEAT)["config_version"] == 0

    def test_capabilities_advertise_ping(self):
        """Core only probes adapters that say they can answer."""
        assert "ping" in make_reporter().frame(AdapterPhase.HEARTBEAT)["capabilities"]

    def test_heartbeat_interval_is_announced(self):
        frame = make_reporter(heartbeat_interval=60).frame(AdapterPhase.HEARTBEAT)
        assert frame["heartbeat_interval_s"] == 60


class TestPublishing:
    @pytest.mark.asyncio
    async def test_publishes_to_the_adapter_subject(self):
        r = make_reporter(adapter_id="modbus-line3")
        await r.publish(AdapterPhase.ONLINE)

        r.client.nc.publish.assert_awaited_once()
        subject, payload = r.client.nc.publish.await_args.args
        assert subject == SUBJECT_ADAPTER_STATUS_PREFIX + "modbus-line3"
        assert json.loads(payload)["phase"] == AdapterPhase.ONLINE.value

    @pytest.mark.asyncio
    async def test_publish_failure_is_swallowed(self):
        """Failing to report must never take down an adapter that is otherwise
        collecting fine."""
        r = make_reporter()
        r.client.nc.publish.side_effect = RuntimeError("broker gone")
        await r.publish(AdapterPhase.HEARTBEAT)

    @pytest.mark.asyncio
    async def test_publish_without_connection_is_safe(self):
        r = make_reporter()
        r.client.nc = None
        await r.publish(AdapterPhase.HEARTBEAT)


class TestDeviceStateTracking:
    def test_transition_counts_errors(self):
        r = make_reporter()
        r.set_device_state("error")
        assert r.counters.device_errors_total == 1

    def test_repeat_state_is_not_counted(self):
        r = make_reporter()
        r.set_device_state("error")
        r.set_device_state("error")
        assert r.counters.device_errors_total == 1

    def test_reconnect_counted(self):
        r = make_reporter()
        r.set_device_state("reconnecting")
        assert r.counters.device_reconnects_total == 1
