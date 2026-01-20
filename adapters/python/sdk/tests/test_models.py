"""Tests for SDK model classes"""

import pytest
from enum import Enum

from sdk.models import DeviceState


class TestDeviceStateEnum:
    """Test DeviceState enum"""

    def test_device_state_is_enum(self):
        """DeviceState should be an Enum"""
        assert issubclass(DeviceState, Enum)

    def test_device_state_disconnected_value(self):
        """DISCONNECTED state should have correct value"""
        assert DeviceState.DISCONNECTED.value == "disconnected"

    def test_device_state_connecting_value(self):
        """CONNECTING state should have correct value"""
        assert DeviceState.CONNECTING.value == "connecting"

    def test_device_state_connected_value(self):
        """CONNECTED state should have correct value"""
        assert DeviceState.CONNECTED.value == "connected"

    def test_device_state_reconnecting_value(self):
        """RECONNECTING state should have correct value"""
        assert DeviceState.RECONNECTING.value == "reconnecting"

    def test_device_state_error_value(self):
        """ERROR state should have correct value"""
        assert DeviceState.ERROR.value == "error"

    def test_device_state_has_all_states(self):
        """DeviceState should have all expected states"""
        expected_states = {
            "DISCONNECTED",
            "CONNECTING",
            "CONNECTED",
            "RECONNECTING",
            "ERROR",
        }
        actual_states = {state.name for state in DeviceState}
        assert actual_states == expected_states

    def test_device_state_string_representation(self):
        """DeviceState should have readable string representation"""
        assert str(DeviceState.CONNECTED) == "DeviceState.CONNECTED"

    def test_device_state_comparison(self):
        """DeviceState should support equality comparison"""
        assert DeviceState.CONNECTED == DeviceState.CONNECTED
        assert DeviceState.CONNECTED != DeviceState.DISCONNECTED
