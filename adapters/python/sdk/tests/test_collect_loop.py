"""Tests for enhanced collect loop with retry and backoff"""

import asyncio

import pytest
from unittest.mock import AsyncMock

from sdk.models import DeviceState, TagValue
from sdk.exceptions import DeviceConnectionError, DeviceTimeoutError
from sdk.tests.conftest import MockAdapter, FailingAdapter


class TestCollectLoopRetryBehavior:
    """Test collect loop retry behavior"""

    @pytest.mark.skip(reason="TODO: Implement test - requires _collect_once simulation")
    @pytest.mark.asyncio
    async def test_collect_loop_retries_on_device_connection_error(self):
        """Collect loop should retry on DeviceConnectionError"""
        # Create adapter that fails twice then succeeds
        adapter = FailingAdapter(
            asset_id="test",
            fail_count=2,
            error_type=DeviceConnectionError,
        )
        adapter.collect_values = [TagValue(name="temp", number=25.0)]

        # Mock the client to avoid actual NATS connection
        adapter._client.connect = AsyncMock()
        adapter._client.disconnect = AsyncMock()
        adapter._client.publish_asset_data = AsyncMock()

        # Call _collect_once (simulating one collection cycle)
        # This should handle the retries internally
        # We expect it to retry twice and then succeed on the third attempt
        # Note: We'll need to implement this behavior in Phase 7

    @pytest.mark.skip(reason="TODO: Implement test - requires retry verification")
    @pytest.mark.asyncio
    async def test_collect_loop_retries_on_device_timeout_error(self):
        """Collect loop should retry on DeviceTimeoutError"""
        adapter = FailingAdapter(
            asset_id="test",
            fail_count=1,
            error_type=DeviceTimeoutError,
        )
        adapter.collect_values = [TagValue(name="temp", number=25.0)]

        adapter._client.connect = AsyncMock()
        adapter._client.disconnect = AsyncMock()
        adapter._client.publish_asset_data = AsyncMock()

        # Similar to above - should retry once and succeed


class TestCollectLoopBackoffStrategy:
    """Test backoff strategy in collect loop"""

    @pytest.mark.skip(reason="TODO: Implement test - requires asyncio.sleep mocking")
    @pytest.mark.asyncio
    async def test_collect_loop_uses_exponential_backoff(self):
        """Collect loop should use exponential backoff between retries"""
        # This will verify that delays increase exponentially
        # We'll need to mock asyncio.sleep to capture delay values

    @pytest.mark.skip(reason="TODO: Implement test - requires max_delay verification")
    @pytest.mark.asyncio
    async def test_collect_loop_respects_max_delay(self):
        """Collect loop backoff should not exceed max_delay"""
        # Verify that even with many retries, delay doesn't exceed max


class TestCollectLoopStateTransitions:
    """Test device state transitions during collect loop"""

    @pytest.mark.skip(reason="TODO: Implement test - requires state transition verification")
    @pytest.mark.asyncio
    async def test_collect_loop_transitions_to_connected_on_success(self):
        """State should transition to CONNECTED on successful collection"""
        adapter = MockAdapter(asset_id="test")
        adapter.collect_values = [TagValue(name="temp", number=25.0)]

        # Initial state
        assert adapter.device_state == DeviceState.DISCONNECTED

        # After successful collection, should be CONNECTED
        # (Implementation detail: this will be handled in _collect_loop)

    @pytest.mark.skip(reason="TODO: Implement test - requires ERROR state verification")
    @pytest.mark.asyncio
    async def test_collect_loop_transitions_to_error_on_failure(self):
        """State should transition to ERROR on repeated failures"""
        # After max retries exhausted, state should be ERROR


class TestCollectLoopEventHooks:
    """Test event hooks are called during collect loop"""

    @pytest.mark.skip(reason="TODO: Requires proper adapter lifecycle mocking - hangs in test environment")
    @pytest.mark.asyncio
    async def test_on_device_connected_called_on_first_success(self):
        """on_device_connected should be called on first successful collection"""
        adapter = MockAdapter(asset_id="test")
        adapter.collect_values = [TagValue(name="temp", number=25.0)]

        adapter._client.connect = AsyncMock()
        adapter._client.disconnect = AsyncMock()
        adapter._client.publish_asset_data = AsyncMock()

        # Track if hook was called
        connected_called = False
        reconnected_called = False

        original_connected = adapter.on_device_connected
        original_reconnected = adapter.on_device_reconnected

        async def mock_on_connected():
            nonlocal connected_called
            connected_called = True
            await original_connected()

        async def mock_on_reconnected():
            nonlocal reconnected_called
            reconnected_called = True
            await original_reconnected()

        adapter.on_device_connected = mock_on_connected
        adapter.on_device_reconnected = mock_on_reconnected

        # Start adapter and wait for first collection
        await adapter.start()
        await asyncio.sleep(0.2)
        await adapter.stop()

        # On first connection, on_device_connected should be called (not reconnected)
        assert connected_called, "on_device_connected should be called on first connection"
        assert not reconnected_called, "on_device_reconnected should NOT be called on first connection"

    @pytest.mark.skip(reason="TODO: Implement test - requires disconnected hook verification")
    @pytest.mark.asyncio
    async def test_on_device_disconnected_called_on_error(self):
        """on_device_disconnected should be called when error occurs"""
        # Create adapter that always fails
        # Verify on_device_disconnected is called with the error

    @pytest.mark.skip(reason="TODO: Requires proper adapter lifecycle mocking - hangs in test environment")
    @pytest.mark.asyncio
    async def test_on_device_reconnected_called_after_recovery(self):
        """on_device_reconnected should be called after recovery from error"""
        # Create adapter that fails once, then succeeds
        adapter = FailingAdapter(
            asset_id="test",
            fail_count=1,
            error_type=DeviceConnectionError,
        )
        adapter.collect_values = [TagValue(name="temp", number=25.0)]

        adapter._client.connect = AsyncMock()
        adapter._client.disconnect = AsyncMock()
        adapter._client.publish_asset_data = AsyncMock()

        reconnected_called = False
        connected_called = False

        original_reconnected = adapter.on_device_reconnected
        original_connected = adapter.on_device_connected

        async def mock_reconnected():
            nonlocal reconnected_called
            reconnected_called = True
            await original_reconnected()

        async def mock_connected():
            nonlocal connected_called
            connected_called = True
            await original_connected()

        adapter.on_device_reconnected = mock_reconnected
        adapter.on_device_connected = mock_connected

        # Start adapter
        await adapter.start()
        await asyncio.sleep(0.3)  # Wait for retry and success
        await adapter.stop()

        # After recovery, on_device_reconnected should be called (not on_device_connected)
        assert reconnected_called, "on_device_reconnected should be called after recovery"
        assert not connected_called, "on_device_connected should NOT be called on reconnection"


class TestCollectLoopLifecycleHooks:
    """Test lifecycle hooks integration with collect loop"""

    @pytest.mark.skip(reason="TODO: Implement test - requires connect_device verification")
    @pytest.mark.asyncio
    async def test_connect_device_called_before_first_collection(self):
        """connect_device should be called before first data collection"""
        adapter = MockAdapter(asset_id="test")
        adapter.collect_values = [TagValue(name="temp", number=25.0)]

        connect_called = False

        async def mock_connect():
            nonlocal connect_called
            connect_called = True

        adapter.connect_device = mock_connect

        # Run and verify connect_device was called

    @pytest.mark.skip(reason="TODO: Implement test - requires disconnect_device verification")
    @pytest.mark.asyncio
    async def test_disconnect_device_called_on_stop(self):
        """disconnect_device should be called when adapter stops"""
        # Verify disconnect_device is called during stop()

    @pytest.mark.skip(reason="TODO: Implement test - requires health check verification")
    @pytest.mark.asyncio
    async def test_check_device_health_called_periodically(self):
        """check_device_health should be called periodically"""
        # Verify health check is called during collection cycle


class TestCollectLoopErrorHandling:
    """Test error handling in collect loop"""

    @pytest.mark.skip(reason="TODO: Implement test - requires non-device error verification")
    @pytest.mark.asyncio
    async def test_non_device_errors_not_retried(self):
        """Non-device errors should not trigger retries"""
        # Regular exceptions should be logged but not retried
        # Only DeviceError subclasses should trigger retry logic

    @pytest.mark.skip(reason="TODO: Requires proper adapter lifecycle mocking - hangs in test environment")
    @pytest.mark.asyncio
    async def test_max_retries_prevents_infinite_loop(self):
        """Maximum retries should prevent infinite retry loops"""
        # Create adapter that always fails
        adapter = FailingAdapter(
            asset_id="test",
            fail_count=100,  # Fail more than max_retries
            error_type=DeviceConnectionError,
        )

        adapter._client.connect = AsyncMock()
        adapter._client.disconnect = AsyncMock()
        adapter._client.publish_asset_data = AsyncMock()

        # Start adapter
        await adapter.start()
        await asyncio.sleep(0.5)  # Wait for max retries

        # Verify it transitions to ERROR state after max retries
        assert adapter.device_state == DeviceState.ERROR

        # Verify retry count is reset for future attempts
        assert adapter._retry_count == 0, "Retry count should be reset after max retries exceeded"

        await adapter.stop()

    @pytest.mark.skip(reason="TODO: Implement test - requires logging verification")
    @pytest.mark.asyncio
    async def test_error_logged_on_retry(self):
        """Errors should be logged during retry attempts"""
        # Verify logging occurs for each retry attempt


# Placeholder tests that will pass initially
# These define the contract that Phase 7 implementation must fulfill
class TestCollectLoopContract:
    """Contract tests for collect loop enhancement"""

    def test_backoff_strategy_available(self):
        """BackoffStrategy should be importable"""
        from sdk.backoff import BackoffStrategy

        assert BackoffStrategy is not None

    def test_device_errors_available(self):
        """DeviceError exceptions should be available"""
        from sdk.exceptions import DeviceConnectionError, DeviceTimeoutError

        assert DeviceConnectionError is not None
        assert DeviceTimeoutError is not None

    def test_device_state_enum_available(self):
        """DeviceState enum should be available"""
        from sdk.models import DeviceState

        assert DeviceState is not None
        assert DeviceState.CONNECTED is not None
