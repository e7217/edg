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


class ForbiddenError(PublishError):
    """The NATS server refused the operation for lack of permission.

    Subclasses PublishError so that existing ``except PublishError`` handlers
    keep working; catch ForbiddenError specifically to distinguish "the
    credentials in the NATS URL lack this grant" from "the request failed".

    NATS reports a denied publish only on the asynchronous error callback: the
    publish is dropped server-side and no reply ever arrives, so without
    correlation the caller would see a bare timeout. See ADR 0007.
    """

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
