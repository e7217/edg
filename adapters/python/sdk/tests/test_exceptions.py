"""Tests for SDK exception classes"""

import pytest

from sdk.exceptions import (
    SDKError,
    ConnectionError,
    PublishError,
    DeviceError,
    DeviceConnectionError,
    DeviceTimeoutError,
)


class TestExceptionHierarchy:
    """Test exception class hierarchy"""

    def test_sdk_error_is_base_exception(self):
        """SDKError should inherit from Exception"""
        assert issubclass(SDKError, Exception)

    def test_device_error_inherits_from_sdk_error(self):
        """DeviceError should inherit from SDKError"""
        assert issubclass(DeviceError, SDKError)

    def test_device_connection_error_inherits_from_device_error(self):
        """DeviceConnectionError should inherit from DeviceError"""
        assert issubclass(DeviceConnectionError, DeviceError)

    def test_device_timeout_error_inherits_from_device_error(self):
        """DeviceTimeoutError should inherit from DeviceError"""
        assert issubclass(DeviceTimeoutError, DeviceError)


class TestDeviceErrorInstantiation:
    """Test device error instantiation and messages"""

    def test_device_error_with_message(self):
        """DeviceError should accept a message"""
        error = DeviceError("test error")
        assert str(error) == "test error"

    def test_device_connection_error_with_message(self):
        """DeviceConnectionError should accept a message"""
        error = DeviceConnectionError("connection failed")
        assert str(error) == "connection failed"

    def test_device_timeout_error_with_message(self):
        """DeviceTimeoutError should accept a message"""
        error = DeviceTimeoutError("operation timed out")
        assert str(error) == "operation timed out"


class TestDeviceErrorCatching:
    """Test exception catching behavior"""

    def test_catch_device_connection_error_as_device_error(self):
        """DeviceConnectionError should be catchable as DeviceError"""
        with pytest.raises(DeviceError):
            raise DeviceConnectionError("test")

    def test_catch_device_timeout_error_as_device_error(self):
        """DeviceTimeoutError should be catchable as DeviceError"""
        with pytest.raises(DeviceError):
            raise DeviceTimeoutError("test")

    def test_catch_device_error_as_sdk_error(self):
        """DeviceError should be catchable as SDKError"""
        with pytest.raises(SDKError):
            raise DeviceError("test")
