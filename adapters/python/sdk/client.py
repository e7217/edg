"""NATS client wrapper"""

from __future__ import annotations

import json
import logging
import re
import time
from urllib.parse import urlsplit, urlunsplit
from typing import TYPE_CHECKING

import nats

from .exceptions import ConnectionError, ForbiddenError, PublishError
from .models import AssetData, AssetRelation

if TYPE_CHECKING:
    from nats.aio.client import Client as NATSClient

logger = logging.getLogger(__name__)

# Matches `Permissions Violation for Publish to "subject"` from nats-server.
_PERM_SUBJECT_RE = re.compile(r'Permissions Violation for (\w+) to "([^"]+)"')


def redact_url(raw: str) -> str:
    """Mask the password in a NATS URL so it can be logged.

    Credentials are carried in the URL (ADR 0007), which is only safe if the
    URL is never echoed verbatim. Unparseable input is returned unchanged
    rather than risking a partial leak from a half-successful parse.
    """
    if not raw:
        return raw

    out = []
    for part in raw.split(","):
        try:
            parsed = urlsplit(part.strip())
        except ValueError:
            out.append(part)
            continue
        if not parsed.hostname or parsed.password is None:
            out.append(part)
            continue
        netloc = f"{parsed.username}:xxxxx@{parsed.hostname}"
        if parsed.port:
            netloc += f":{parsed.port}"
        out.append(urlunsplit((parsed.scheme, netloc, parsed.path, parsed.query, parsed.fragment)))
    return ",".join(out)

# NATS topics
TOPIC_ASSET_DATA = "platform.data.asset"

# NATS relation subjects
SUBJECT_RELATION_CREATE = "platform.meta.relation.create"
SUBJECT_RELATION_GET = "platform.meta.relation.get"
SUBJECT_RELATION_LIST = "platform.meta.relation.list"
SUBJECT_RELATION_DELETE = "platform.meta.relation.delete"


class NATSClientWrapper:
    """NATS client wrapper

    Manages connections and data publishing.
    """

    def __init__(
        self,
        url: str = "nats://localhost:4222",
        name: str | None = None,
        max_reconnect_attempts: int = -1,
        reconnect_time_wait: float = 2.0,
        connect_timeout: float = 2.0,
    ):
        """
        Args:
            url: NATS server URL
            name: Client name (for logging)
            max_reconnect_attempts: Maximum reconnection attempts (default: -1 for unlimited)
            reconnect_time_wait: Time to wait between reconnect attempts in seconds (default: 2.0)
            connect_timeout: Connection timeout in seconds (default: 2.0)
        """
        self.url = url
        self.name = name
        self.max_reconnect_attempts = max_reconnect_attempts
        self.reconnect_time_wait = reconnect_time_wait
        self.connect_timeout = connect_timeout
        self._nc: NATSClient | None = None
        # subject -> (op, monotonic timestamp) for subjects the server refused.
        self._permission_errors: dict[str, tuple[str, float]] = {}

    # A violation stays attributable for long enough to outlive the gap
    # between the server refusing a publish and the caller's request timing
    # out. Entries are keyed by subject, so a generous window costs nothing.
    _PERMISSION_WINDOW_S = 30.0
    # An adapter uses a small fixed set of subjects; this only guards against a
    # pathological caller.
    _MAX_TRACKED_VIOLATIONS = 32

    def _record_permission_error(self, message: str) -> tuple[str, str] | None:
        """Note a permission violation. Returns (op, subject) when the message
        was one, else None."""
        match = _PERM_SUBJECT_RE.search(message)
        if not match:
            return None
        op, subject = match.group(1).lower(), match.group(2)
        if len(self._permission_errors) >= self._MAX_TRACKED_VIOLATIONS:
            self._permission_errors.clear()
        self._permission_errors[subject] = (op, time.monotonic())
        return op, subject

    def _consume_permission(self, subject: str) -> str | None:
        """Return the refused operation for a subject, consuming the entry so a
        later unrelated timeout is not mislabeled."""
        hit = self._permission_errors.pop(subject, None)
        if hit is None:
            return None
        op, at = hit
        if time.monotonic() - at > self._PERMISSION_WINDOW_S:
            return None
        return op

    async def _request(self, subject: str, payload: bytes, timeout: float = 5.0):
        """Issue a request, converting a refusal into ForbiddenError.

        A denied publish is dropped by the server, so the request simply never
        gets a reply; the reason arrives only via the async error callback.
        """
        try:
            return await self._nc.request(subject, payload, timeout=timeout)
        except Exception as exc:
            op = self._consume_permission(subject)
            if op is not None:
                raise ForbiddenError(
                    f"permission denied: not authorized to {op} {subject}; "
                    "check the credentials in the NATS URL and the role's subject permissions"
                ) from exc
            raise

    @property
    def is_connected(self) -> bool:
        """Check connection status"""
        return self._nc is not None and self._nc.is_connected

    async def connect(self) -> None:
        """Connect to NATS server"""
        if self.is_connected:
            return

        try:
            self._nc = await nats.connect(
                self.url,
                name=self.name,
                max_reconnect_attempts=self.max_reconnect_attempts,
                reconnect_time_wait=self.reconnect_time_wait,
                connect_timeout=self.connect_timeout,
                error_cb=self._error_callback,
                disconnected_cb=self._disconnected_callback,
                reconnected_cb=self._reconnected_callback,
            )
            logger.info(f"NATS connected: {redact_url(self.url)}")
        except Exception as e:
            raise ConnectionError(f"NATS connection failed: {e}") from e

    async def disconnect(self) -> None:
        """Disconnect from NATS"""
        if self._nc is not None:
            await self._nc.drain()
            self._nc = None
            logger.info("NATS disconnected")

    async def publish_asset_data(self, data: AssetData) -> None:
        """Publish asset data

        Args:
            data: AssetData to publish

        Raises:
            PublishError: When publish fails
        """
        if not self.is_connected:
            raise PublishError("NATS not connected")

        try:
            payload = json.dumps(data.to_dict()).encode()
            await self._nc.publish(TOPIC_ASSET_DATA, payload)
            logger.debug(f"Published data: {data.asset_id}, {len(data.values)} tags")
        except ForbiddenError:
            # Already carries the subject and the reason; re-wrapping
            # as a bare PublishError would erase both.
            raise
        except Exception as e:
            raise PublishError(f"Data publish failed: {e}") from e

    async def _error_callback(self, e: Exception) -> None:
        """Error callback"""
        recorded = self._record_permission_error(str(e))
        if recorded is not None:
            op, subject = recorded
            # Distinct from a generic NATS error: a denied subscription has no
            # other symptom at all, the adapter simply never receives anything.
            logger.error(
                "NATS permission denied: not authorized to %s %s; "
                "the credentials in the NATS URL lack this grant (see the role matrix in ADR 0007)",
                op,
                subject,
            )
            return
        logger.error(f"NATS error: {e}")

    async def _disconnected_callback(self) -> None:
        """Disconnected callback"""
        logger.warning("NATS disconnected")

    async def _reconnected_callback(self) -> None:
        """Reconnected callback"""
        logger.info("NATS reconnected")

    async def create_relation(
        self,
        source_asset_id: str,
        target_asset_id: str,
        relation_type: str,
        metadata: dict[str, str] | None = None,
    ) -> AssetRelation:
        """Create asset relation

        Args:
            source_asset_id: Source asset ID
            target_asset_id: Target asset ID
            relation_type: Relation type (partOf, connectedTo, locatedIn)
            metadata: Optional metadata dictionary

        Returns:
            Created AssetRelation

        Raises:
            PublishError: When request fails
        """
        if not self.is_connected:
            raise PublishError("NATS not connected")

        request_data = {
            "source_asset_id": source_asset_id,
            "target_asset_id": target_asset_id,
            "relation_type": relation_type,
        }
        if metadata:
            request_data["metadata"] = metadata

        try:
            payload = json.dumps(request_data).encode()
            response = await self._request(SUBJECT_RELATION_CREATE, payload)
            result = json.loads(response.data.decode())

            if not result.get("success"):
                raise PublishError(f"Create relation failed: {result.get('error')}")

            data = result["data"]
            return AssetRelation(
                id=data["id"],
                source_asset_id=data["source_asset_id"],
                target_asset_id=data["target_asset_id"],
                relation_type=data["relation_type"],
                created_at=data["created_at"],
                metadata=data.get("metadata"),
            )
        except ForbiddenError:
            # Already carries the subject and the reason; re-wrapping
            # as a bare PublishError would erase both.
            raise
        except Exception as e:
            raise PublishError(f"Create relation failed: {e}") from e

    async def get_relation(self, relation_id: str) -> AssetRelation | None:
        """Get relation by ID

        Args:
            relation_id: Relation ID

        Returns:
            AssetRelation or None if not found

        Raises:
            PublishError: When request fails
        """
        if not self.is_connected:
            raise PublishError("NATS not connected")

        try:
            payload = json.dumps({"id": relation_id}).encode()
            response = await self._request(SUBJECT_RELATION_GET, payload)
            result = json.loads(response.data.decode())

            if not result.get("success"):
                error = result.get("error", "")
                if "not found" in error:
                    return None
                raise PublishError(f"Get relation failed: {error}")

            data = result["data"]
            return AssetRelation(
                id=data["id"],
                source_asset_id=data["source_asset_id"],
                target_asset_id=data["target_asset_id"],
                relation_type=data["relation_type"],
                created_at=data["created_at"],
                metadata=data.get("metadata"),
            )
        except ForbiddenError:
            # Already carries the subject and the reason; re-wrapping
            # as a bare PublishError would erase both.
            raise
        except Exception as e:
            raise PublishError(f"Get relation failed: {e}") from e

    async def list_relations(
        self,
        asset_id: str,
        relation_type: str | None = None,
        direction: str = "both",
    ) -> list[AssetRelation]:
        """List relations by asset ID

        Args:
            asset_id: Asset ID to find relations for
            relation_type: Optional filter by relation type
            direction: Direction filter (outgoing, incoming, both)

        Returns:
            List of AssetRelations

        Raises:
            PublishError: When request fails
        """
        if not self.is_connected:
            raise PublishError("NATS not connected")

        request_data = {"asset_id": asset_id, "direction": direction}
        if relation_type:
            request_data["relation_type"] = relation_type

        try:
            payload = json.dumps(request_data).encode()
            response = await self._request(SUBJECT_RELATION_LIST, payload)
            result = json.loads(response.data.decode())

            if not result.get("success"):
                raise PublishError(f"List relations failed: {result.get('error')}")

            data = result.get("data", [])
            return [
                AssetRelation(
                    id=item["id"],
                    source_asset_id=item["source_asset_id"],
                    target_asset_id=item["target_asset_id"],
                    relation_type=item["relation_type"],
                    created_at=item["created_at"],
                    metadata=item.get("metadata"),
                )
                for item in data
            ]
        except ForbiddenError:
            # Already carries the subject and the reason; re-wrapping
            # as a bare PublishError would erase both.
            raise
        except Exception as e:
            raise PublishError(f"List relations failed: {e}") from e

    async def delete_relation(self, relation_id: str) -> None:
        """Delete relation

        Args:
            relation_id: Relation ID to delete

        Raises:
            PublishError: When request fails
        """
        if not self.is_connected:
            raise PublishError("NATS not connected")

        try:
            payload = json.dumps({"id": relation_id}).encode()
            response = await self._request(SUBJECT_RELATION_DELETE, payload)
            result = json.loads(response.data.decode())

            if not result.get("success"):
                raise PublishError(f"Delete relation failed: {result.get('error')}")

            logger.debug(f"Deleted relation: {relation_id}")
        except ForbiddenError:
            # Already carries the subject and the reason; re-wrapping
            # as a bare PublishError would erase both.
            raise
        except Exception as e:
            raise PublishError(f"Delete relation failed: {e}") from e
