"""Tests for BaseAdapter"""

import pytest

from sdk.models import DeviceState
from sdk.exceptions import DeviceConnectionError


class TestAdapterDeviceState:
    """Test device state tracking in BaseAdapter"""

    def test_adapter_initial_device_state(self, mock_adapter):
        """Adapter should start with DISCONNECTED state"""
        assert mock_adapter.device_state == DeviceState.DISCONNECTED

    @pytest.mark.asyncio
    async def test_adapter_device_state_property_readonly(self, mock_adapter):
        """device_state should be read-only property"""
        # Verify it's a property
        assert hasattr(type(mock_adapter), "device_state")
        assert isinstance(getattr(type(mock_adapter), "device_state"), property)

        # Attempting to set directly should fail
        with pytest.raises(AttributeError):
            mock_adapter.device_state = DeviceState.CONNECTED

    @pytest.mark.asyncio
    async def test_set_device_state_internal(self, mock_adapter):
        """_set_device_state should update internal state"""
        await mock_adapter._set_device_state(DeviceState.CONNECTING)
        assert mock_adapter.device_state == DeviceState.CONNECTING

        await mock_adapter._set_device_state(DeviceState.CONNECTED)
        assert mock_adapter.device_state == DeviceState.CONNECTED

    @pytest.mark.asyncio
    async def test_set_device_state_transitions(self, mock_adapter):
        """Test state transitions"""
        # DISCONNECTED -> CONNECTING
        await mock_adapter._set_device_state(DeviceState.CONNECTING)
        assert mock_adapter.device_state == DeviceState.CONNECTING

        # CONNECTING -> CONNECTED
        await mock_adapter._set_device_state(DeviceState.CONNECTED)
        assert mock_adapter.device_state == DeviceState.CONNECTED

        # CONNECTED -> RECONNECTING
        await mock_adapter._set_device_state(DeviceState.RECONNECTING)
        assert mock_adapter.device_state == DeviceState.RECONNECTING

        # RECONNECTING -> CONNECTED
        await mock_adapter._set_device_state(DeviceState.CONNECTED)
        assert mock_adapter.device_state == DeviceState.CONNECTED

        # CONNECTED -> ERROR
        await mock_adapter._set_device_state(DeviceState.ERROR)
        assert mock_adapter.device_state == DeviceState.ERROR


class TestAdapterStateInitialization:
    """Test adapter state initialization"""

    def test_new_adapter_starts_disconnected(self):
        """New adapter instance should have DISCONNECTED state"""
        from sdk.tests.conftest import MockAdapter

        adapter = MockAdapter(asset_id="test")
        assert adapter.device_state == DeviceState.DISCONNECTED

    def test_multiple_adapters_independent_states(self):
        """Multiple adapter instances should have independent states"""
        from sdk.tests.conftest import MockAdapter

        adapter1 = MockAdapter(asset_id="test1")
        adapter2 = MockAdapter(asset_id="test2")

        assert adapter1.device_state == DeviceState.DISCONNECTED
        assert adapter2.device_state == DeviceState.DISCONNECTED

        # Changing one should not affect the other
        # (This test will be meaningful once we have state-changing methods)


class TestAdapterStateAccess:
    """Test device_state property access"""

    def test_device_state_returns_enum(self, mock_adapter):
        """device_state should return DeviceState enum"""
        state = mock_adapter.device_state
        assert isinstance(state, DeviceState)

    def test_device_state_default_value(self, mock_adapter):
        """device_state should have correct default value"""
        assert mock_adapter.device_state == DeviceState.DISCONNECTED
        assert mock_adapter.device_state.value == "disconnected"


class TestAdapterLifecycleHooks:
    """Test adapter lifecycle hooks"""

    @pytest.mark.asyncio
    async def test_connect_device_hook_exists(self, mock_adapter):
        """connect_device hook should exist"""
        assert hasattr(mock_adapter, "connect_device")
        assert callable(mock_adapter.connect_device)

    @pytest.mark.asyncio
    async def test_disconnect_device_hook_exists(self, mock_adapter):
        """disconnect_device hook should exist"""
        assert hasattr(mock_adapter, "disconnect_device")
        assert callable(mock_adapter.disconnect_device)

    @pytest.mark.asyncio
    async def test_check_device_health_hook_exists(self, mock_adapter):
        """check_device_health hook should exist"""
        assert hasattr(mock_adapter, "check_device_health")
        assert callable(mock_adapter.check_device_health)

    @pytest.mark.asyncio
    async def test_connect_device_default_implementation(self, mock_adapter):
        """connect_device should have default no-op implementation"""
        # Should not raise any exception
        await mock_adapter.connect_device()

    @pytest.mark.asyncio
    async def test_disconnect_device_default_implementation(self, mock_adapter):
        """disconnect_device should have default no-op implementation"""
        # Should not raise any exception
        await mock_adapter.disconnect_device()

    @pytest.mark.asyncio
    async def test_check_device_health_default_implementation(self, mock_adapter):
        """check_device_health should have default no-op implementation"""
        # Should not raise any exception
        await mock_adapter.check_device_health()


class TestAdapterLifecycleHookOverrides:
    """Test adapter lifecycle hook overrides"""

    @pytest.mark.asyncio
    async def test_connect_device_can_be_overridden(self):
        """Subclass should be able to override connect_device"""
        from sdk.tests.conftest import MockAdapter

        class CustomAdapter(MockAdapter):
            async def connect_device(self):
                self.connect_device_called = True

        adapter = CustomAdapter(asset_id="test")
        await adapter.connect_device()
        assert adapter.connect_device_called is True

    @pytest.mark.asyncio
    async def test_disconnect_device_can_be_overridden(self):
        """Subclass should be able to override disconnect_device"""
        from sdk.tests.conftest import MockAdapter

        class CustomAdapter(MockAdapter):
            async def disconnect_device(self):
                self.disconnect_device_called = True

        adapter = CustomAdapter(asset_id="test")
        await adapter.disconnect_device()
        assert adapter.disconnect_device_called is True

    @pytest.mark.asyncio
    async def test_check_device_health_can_be_overridden(self):
        """Subclass should be able to override check_device_health"""
        from sdk.tests.conftest import MockAdapter

        class CustomAdapter(MockAdapter):
            async def check_device_health(self):
                self.check_device_health_called = True

        adapter = CustomAdapter(asset_id="test")
        await adapter.check_device_health()
        assert adapter.check_device_health_called is True


class TestAdapterEventHooks:
    """Test adapter event hooks"""

    @pytest.mark.asyncio
    async def test_on_device_connected_hook_exists(self, mock_adapter):
        """on_device_connected hook should exist"""
        assert hasattr(mock_adapter, "on_device_connected")
        assert callable(mock_adapter.on_device_connected)

    @pytest.mark.asyncio
    async def test_on_device_disconnected_hook_exists(self, mock_adapter):
        """on_device_disconnected hook should exist"""
        assert hasattr(mock_adapter, "on_device_disconnected")
        assert callable(mock_adapter.on_device_disconnected)

    @pytest.mark.asyncio
    async def test_on_device_reconnected_hook_exists(self, mock_adapter):
        """on_device_reconnected hook should exist"""
        assert hasattr(mock_adapter, "on_device_reconnected")
        assert callable(mock_adapter.on_device_reconnected)

    @pytest.mark.asyncio
    async def test_on_device_connected_default_implementation(self, mock_adapter):
        """on_device_connected should have default no-op implementation"""
        # Should not raise any exception
        await mock_adapter.on_device_connected()

    @pytest.mark.asyncio
    async def test_on_device_disconnected_default_implementation(self, mock_adapter):
        """on_device_disconnected should have default no-op implementation"""
        # Should not raise any exception (with or without error parameter)
        await mock_adapter.on_device_disconnected()
        await mock_adapter.on_device_disconnected(error=Exception("test"))

    @pytest.mark.asyncio
    async def test_on_device_reconnected_default_implementation(self, mock_adapter):
        """on_device_reconnected should have default no-op implementation"""
        # Should not raise any exception
        await mock_adapter.on_device_reconnected()


class TestAdapterEventHookOverrides:
    """Test adapter event hook overrides"""

    @pytest.mark.asyncio
    async def test_on_device_connected_can_be_overridden(self):
        """Subclass should be able to override on_device_connected"""
        from sdk.tests.conftest import MockAdapter

        class CustomAdapter(MockAdapter):
            async def on_device_connected(self):
                self.on_device_connected_called = True

        adapter = CustomAdapter(asset_id="test")
        await adapter.on_device_connected()
        assert adapter.on_device_connected_called is True

    @pytest.mark.asyncio
    async def test_on_device_disconnected_can_be_overridden(self):
        """Subclass should be able to override on_device_disconnected"""
        from sdk.tests.conftest import MockAdapter

        class CustomAdapter(MockAdapter):
            async def on_device_disconnected(self, error=None):
                self.on_device_disconnected_called = True
                self.last_error = error

        adapter = CustomAdapter(asset_id="test")
        test_error = DeviceConnectionError("test")
        await adapter.on_device_disconnected(error=test_error)
        assert adapter.on_device_disconnected_called is True
        assert adapter.last_error == test_error

    @pytest.mark.asyncio
    async def test_on_device_reconnected_can_be_overridden(self):
        """Subclass should be able to override on_device_reconnected"""
        from sdk.tests.conftest import MockAdapter

        class CustomAdapter(MockAdapter):
            async def on_device_reconnected(self):
                self.on_device_reconnected_called = True

        adapter = CustomAdapter(asset_id="test")
        await adapter.on_device_reconnected()
        assert adapter.on_device_reconnected_called is True
