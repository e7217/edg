"""Permission-denial diagnostics (ADR 0007).

Credentials ride in the NATS URL, so the SDK needs no credential parameters.
What it does need is to explain a refusal: NATS reports a denied publish only
on the asynchronous error callback, so a request/reply would otherwise surface
as a bare timeout after 5 seconds — indistinguishable from an unreachable core.
"""

from unittest.mock import AsyncMock

import pytest

from ..client import NATSClientWrapper, redact_url
from ..exceptions import ForbiddenError, PublishError


PERM_ERROR = (
    'nats: Permissions Violation for Publish to "platform.meta.relation.create"'
)


class TestRedactURL:
    """A URL carrying credentials must never be logged verbatim."""

    @pytest.mark.parametrize(
        "raw,expected",
        [
            ("nats://localhost:4222", "nats://localhost:4222"),
            ("nats://adapter:s3cr3t@localhost:4222", "nats://adapter:xxxxx@localhost:4222"),
            ("nats://adapter@localhost:4222", "nats://adapter@localhost:4222"),
            (
                "nats://a:b@h1:4222,nats://a:b@h2:4222",
                "nats://a:xxxxx@h1:4222,nats://a:xxxxx@h2:4222",
            ),
            ("", ""),
            ("not a url", "not a url"),
        ],
    )
    def test_redacts_password_only(self, raw, expected):
        assert redact_url(raw) == expected


class TestPermissionTracking:
    def test_error_callback_records_violation(self):
        client = NATSClientWrapper()
        client._record_permission_error(PERM_ERROR)

        op = client._consume_permission("platform.meta.relation.create")
        assert op == "publish"

    def test_entry_is_consumed_once(self):
        """A later, unrelated timeout on the same subject must not be
        mislabeled as a permission problem."""
        client = NATSClientWrapper()
        client._record_permission_error(PERM_ERROR)

        assert client._consume_permission("platform.meta.relation.create") == "publish"
        assert client._consume_permission("platform.meta.relation.create") is None

    def test_non_permission_error_is_ignored(self):
        client = NATSClientWrapper()
        client._record_permission_error("nats: connection closed")
        assert client._consume_permission("platform.meta.relation.create") is None

    def test_tracker_is_bounded(self):
        client = NATSClientWrapper()
        for i in range(100):
            client._record_permission_error(
                f'Permissions Violation for Publish to "subject.{i}"'
            )
        assert len(client._permission_errors) <= client._MAX_TRACKED_VIOLATIONS


class TestForbiddenSurfacing:
    @pytest.mark.asyncio
    async def test_denied_request_raises_forbidden(self):
        """The whole point: a refused request must name its cause instead of
        surfacing as a timeout."""
        client = NATSClientWrapper()
        client._nc = AsyncMock()
        client._nc.is_connected = True
        client._nc.request = AsyncMock(side_effect=TimeoutError())

        # The server refuses the publish and reports it out of band.
        client._record_permission_error(PERM_ERROR)

        with pytest.raises(ForbiddenError) as exc:
            await client.create_relation("a", "b", "partOf")

        assert "platform.meta.relation.create" in str(exc.value)
        assert "permission" in str(exc.value).lower()

    @pytest.mark.asyncio
    async def test_ordinary_timeout_is_not_forbidden(self):
        client = NATSClientWrapper()
        client._nc = AsyncMock()
        client._nc.is_connected = True
        client._nc.request = AsyncMock(side_effect=TimeoutError())

        with pytest.raises(PublishError) as exc:
            await client.create_relation("a", "b", "partOf")

        assert not isinstance(exc.value, ForbiddenError)

    def test_forbidden_is_an_sdk_error(self):
        """Existing `except PublishError` handlers must keep working."""
        assert issubclass(ForbiddenError, PublishError)
