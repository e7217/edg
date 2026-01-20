"""SDK exception classes"""


class SDKError(Exception):
    """SDK base exception"""

    pass


class ConnectionError(SDKError):
    """NATS connection failure"""

    pass


class PublishError(SDKError):
    """Data publish failure"""

    pass


class DeviceError(SDKError):
    """Device base exception - for device-specific errors"""

    pass


class DeviceConnectionError(DeviceError):
    """Device connection failure - retryable"""

    pass


class DeviceTimeoutError(DeviceError):
    """Device timeout - retryable"""

    pass
