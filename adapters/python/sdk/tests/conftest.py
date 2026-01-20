"""Pytest configuration and fixtures"""

import pytest
from typing import Any

from sdk.adapter import BaseAdapter
from sdk.models import TagValue


class MockAdapter(BaseAdapter):
    """Mock adapter for testing

    Tracks method calls and provides controlled behavior for testing.
    """

    def __init__(self, *args: Any, **kwargs: Any):
        super().__init__(*args, **kwargs)

        # Track method calls
        self.collect_called = False
        self.on_start_called = False
        self.on_stop_called = False
        self.connect_device_called = False
        self.disconnect_device_called = False
        self.check_device_health_called = False
        self.on_device_connected_called = False
        self.on_device_disconnected_called = False
        self.on_device_reconnected_called = False

        # Control behavior
        self.collect_values: list[TagValue] = []
        self.should_fail = False
        self.fail_count = 0
        self._current_fail_count = 0

    async def collect(self) -> list[TagValue]:
        """Mock collect implementation"""
        self.collect_called = True

        if self.should_fail and self._current_fail_count < self.fail_count:
            self._current_fail_count += 1
            raise Exception("Mock collection error")

        return self.collect_values

    async def on_start(self) -> None:
        """Track on_start calls"""
        self.on_start_called = True
        await super().on_start()

    async def on_stop(self) -> None:
        """Track on_stop calls"""
        self.on_stop_called = True
        await super().on_stop()


class FailingAdapter(MockAdapter):
    """Adapter that fails a specified number of times before succeeding"""

    def __init__(self, *args: Any, fail_count: int = 0, error_type: type = Exception, **kwargs: Any):
        super().__init__(*args, **kwargs)
        self.fail_count = fail_count
        self.error_type = error_type
        self._current_fail_count = 0
        self.retry_count = 0

    async def collect(self) -> list[TagValue]:
        """Mock collect that fails then succeeds"""
        if self._current_fail_count < self.fail_count:
            self._current_fail_count += 1
            self.retry_count += 1
            raise self.error_type(f"Mock error {self._current_fail_count}")

        return self.collect_values


@pytest.fixture
def mock_adapter():
    """Create a mock adapter for testing"""
    return MockAdapter(
        asset_id="test-asset",
        nats_url="nats://localhost:4222",
        collect_interval=0.1,
    )


@pytest.fixture
def failing_adapter():
    """Create a failing adapter for testing retry logic"""
    return FailingAdapter(
        asset_id="test-asset",
        nats_url="nats://localhost:4222",
        collect_interval=0.1,
        fail_count=2,
    )
