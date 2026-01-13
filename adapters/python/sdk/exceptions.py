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
