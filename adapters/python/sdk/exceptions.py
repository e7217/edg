"""SDK 예외 클래스"""


class SDKError(Exception):
    """SDK 기본 예외"""

    pass


class ConnectionError(SDKError):
    """NATS 연결 실패"""

    pass


class PublishError(SDKError):
    """데이터 발행 실패"""

    pass
