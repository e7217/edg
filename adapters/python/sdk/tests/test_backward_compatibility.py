"""Backward compatibility tests"""

import pytest
from unittest.mock import AsyncMock

from sdk import BaseAdapter, TagValue


class SimpleAdapter(BaseAdapter):
    """Simple adapter using only original API (no new features)"""

    async def collect(self) -> list[TagValue]:
        """Basic collect implementation"""
        return [
            TagValue(name="temperature", number=25.0, unit="°C"),
            TagValue(name="humidity", number=60.0, unit="%"),
        ]


class AdapterWithLifecycleHooks(BaseAdapter):
    """Adapter that only uses the original lifecycle hooks"""

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.start_called = False
        self.stop_called = False

    async def collect(self) -> list[TagValue]:
        return [TagValue(name="value", number=1.0)]

    async def on_start(self):
        """Use original on_start hook"""
        self.start_called = True

    async def on_stop(self):
        """Use original on_stop hook"""
        self.stop_called = True


class TestBackwardCompatibility:
    """Test that existing adapters work without modification"""

    def test_simple_adapter_can_be_instantiated(self):
        """Simple adapter should work with basic parameters"""
        adapter = SimpleAdapter(
            asset_id="test",
            nats_url="nats://localhost:4222",
            collect_interval=1.0,
        )
        assert adapter.asset_id == "test"
        assert adapter.nats_url == "nats://localhost:4222"
        assert adapter.collect_interval == 1.0

    def test_adapter_with_metadata(self):
        """Adapter should accept metadata parameter"""
        adapter = SimpleAdapter(
            asset_id="test",
            metadata={"location": "factory-a"},
        )
        assert adapter.metadata == {"location": "factory-a"}

    @pytest.mark.asyncio
    async def test_collect_method_works(self):
        """Basic collect method should work"""
        adapter = SimpleAdapter(asset_id="test")
        values = await adapter.collect()
        assert len(values) == 2
        assert values[0].name == "temperature"
        assert values[1].name == "humidity"

    @pytest.mark.asyncio
    async def test_original_lifecycle_hooks_work(self):
        """Original on_start and on_stop hooks should work"""
        adapter = AdapterWithLifecycleHooks(asset_id="test")

        # Mock NATS client to avoid connection
        adapter._client.connect = AsyncMock()
        adapter._client.disconnect = AsyncMock()

        # Call lifecycle hooks directly
        await adapter.on_start()
        assert adapter.start_called is True

        await adapter.on_stop()
        assert adapter.stop_called is True

    def test_new_device_features_are_optional(self):
        """New device recovery features should not be required"""
        # Create simple adapter without using any new features
        adapter = SimpleAdapter(asset_id="test")

        # Verify new attributes exist but don't break existing functionality
        assert hasattr(adapter, "device_state")
        assert hasattr(adapter, "_backoff")
        assert hasattr(adapter, "_retry_count")

        # Verify new methods exist but have default implementations
        assert hasattr(adapter, "connect_device")
        assert hasattr(adapter, "disconnect_device")
        assert hasattr(adapter, "check_device_health")
        assert hasattr(adapter, "on_device_connected")
        assert hasattr(adapter, "on_device_disconnected")
        assert hasattr(adapter, "on_device_reconnected")

    @pytest.mark.asyncio
    async def test_new_hooks_have_noop_defaults(self):
        """New lifecycle/event hooks should have no-op default implementations"""
        adapter = SimpleAdapter(asset_id="test")

        # All new hooks should be callable and not raise exceptions
        await adapter.connect_device()
        await adapter.disconnect_device()
        await adapter.check_device_health()
        await adapter.on_device_connected()
        await adapter.on_device_disconnected()
        await adapter.on_device_reconnected()

        # No assertions needed - just verify they don't crash


class TestAPIConsistency:
    """Test that the API is consistent and predictable"""

    def test_baseadapter_signature_unchanged(self):
        """BaseAdapter __init__ signature should be unchanged"""
        import inspect

        sig = inspect.signature(BaseAdapter.__init__)
        params = list(sig.parameters.keys())

        # Original required parameters should still be present
        assert "self" in params
        assert "asset_id" in params
        assert "nats_url" in params
        assert "collect_interval" in params
        assert "metadata" in params

    def test_collect_method_signature_unchanged(self):
        """collect method signature should be unchanged"""
        import inspect

        # Check on a concrete implementation
        sig = inspect.signature(SimpleAdapter.collect)
        params = list(sig.parameters.keys())

        assert "self" in params
        # Should return list[TagValue]
        assert sig.return_annotation.__origin__ == list  # type: ignore

    def test_exported_symbols_include_originals(self):
        """All original exported symbols should still be available"""
        from sdk import (
            TagValue,
            AssetData,
            RelationType,
            AssetRelation,
            BaseAdapter,
            SDKError,
            ConnectionError,
            PublishError,
        )

        # Verify they can be imported
        assert TagValue is not None
        assert AssetData is not None
        assert RelationType is not None
        assert AssetRelation is not None
        assert BaseAdapter is not None
        assert SDKError is not None
        assert ConnectionError is not None
        assert PublishError is not None

    def test_exported_symbols_include_new_additions(self):
        """New exported symbols should be available"""
        from sdk import (
            DeviceState,
            BackoffStrategy,
            DeviceError,
            DeviceConnectionError,
            DeviceTimeoutError,
        )

        # Verify new symbols can be imported
        assert DeviceState is not None
        assert BackoffStrategy is not None
        assert DeviceError is not None
        assert DeviceConnectionError is not None
        assert DeviceTimeoutError is not None
