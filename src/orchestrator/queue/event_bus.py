"""Redis Streams event bus for publish/subscribe.

Implements the Event_Bus using Redis Streams with:
- Event envelope schema: event_type, source_component, timestamp,
  correlation_id, payload, severity, category
- Stream groups for consumer isolation (multiple components can
  independently consume events)
- Named streams per event category: events:task_lifecycle,
  events:agent_action, events:approval, events:system_health,
  events:security

Requirements: 6.4, 6.5
"""

from __future__ import annotations

import json
import logging
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from enum import StrEnum
from typing import Any

import redis.asyncio as redis

logger = logging.getLogger(__name__)


class EventSeverity(StrEnum):
    """Severity levels for events."""

    DEBUG = "debug"
    INFO = "info"
    WARNING = "warning"
    ERROR = "error"
    CRITICAL = "critical"


class EventCategory(StrEnum):
    """Event categories mapping to stream names."""

    TASK_LIFECYCLE = "task_lifecycle"
    AGENT_ACTION = "agent_action"
    APPROVAL = "approval"
    SYSTEM_HEALTH = "system_health"
    SECURITY = "security"


# Stream names keyed by category
STREAM_NAMES: dict[EventCategory, str] = {
    EventCategory.TASK_LIFECYCLE: "events:task_lifecycle",
    EventCategory.AGENT_ACTION: "events:agent_action",
    EventCategory.APPROVAL: "events:approval",
    EventCategory.SYSTEM_HEALTH: "events:system_health",
    EventCategory.SECURITY: "events:security",
}


@dataclass(frozen=True)
class EventEnvelope:
    """Standard event envelope for all events published to the bus.

    Attributes:
        event_type: Dot-separated event identifier (e.g. task_lifecycle.queued).
        source_component: Module that produced the event (e.g. state_machine).
        timestamp: ISO-8601 UTC timestamp of event creation.
        correlation_id: UUID linking related events across the system.
        payload: Arbitrary event-specific data.
        severity: Event severity level.
        category: Event category determining the target stream.
    """

    event_type: str
    source_component: str
    timestamp: str
    correlation_id: str
    payload: dict[str, Any]
    severity: EventSeverity
    category: EventCategory

    def to_stream_fields(self) -> dict[str, str]:
        """Serialize the envelope to a flat dict suitable for XADD."""
        return {
            "event_type": self.event_type,
            "source_component": self.source_component,
            "timestamp": self.timestamp,
            "correlation_id": self.correlation_id,
            "payload": json.dumps(self.payload),
            "severity": self.severity.value,
            "category": self.category.value,
        }

    @classmethod
    def from_stream_fields(cls, fields: dict[bytes | str, bytes | str]) -> EventEnvelope:
        """Deserialize stream fields back into an EventEnvelope."""
        # Redis returns bytes or str depending on decode_responses setting
        def _decode(value: bytes | str) -> str:
            return value.decode() if isinstance(value, bytes) else value

        return cls(
            event_type=_decode(fields[b"event_type" if b"event_type" in fields else "event_type"]),
            source_component=_decode(
                fields[b"source_component" if b"source_component" in fields else "source_component"]
            ),
            timestamp=_decode(fields[b"timestamp" if b"timestamp" in fields else "timestamp"]),
            correlation_id=_decode(
                fields[b"correlation_id" if b"correlation_id" in fields else "correlation_id"]
            ),
            payload=json.loads(
                _decode(fields[b"payload" if b"payload" in fields else "payload"])
            ),
            severity=EventSeverity(
                _decode(fields[b"severity" if b"severity" in fields else "severity"])
            ),
            category=EventCategory(
                _decode(fields[b"category" if b"category" in fields else "category"])
            ),
        )


@dataclass(frozen=True)
class ConsumedEvent:
    """An event read from a stream with its message ID."""

    message_id: str
    envelope: EventEnvelope


class RedisEventBus:
    """Redis Streams-based event bus.

    Provides publish/subscribe semantics with consumer groups for isolation.
    Each subscribing component creates its own consumer group so that
    multiple consumers can independently process the same stream of events.
    """

    def __init__(self, client: redis.Redis) -> None:
        self._client = client

    async def publish(self, stream: str, event: dict[str, Any]) -> str:
        """Publish an event to a Redis Stream.

        This method satisfies the IEventPublisher protocol used by the
        state machine engine. It accepts a raw event dict and wraps it
        in the standard envelope if not already wrapped.

        Args:
            stream: Target stream name (e.g. "events:task_lifecycle").
            event: Event data. If it contains all envelope fields, it is
                published directly. Otherwise it is wrapped in an envelope.

        Returns:
            The Redis message ID assigned to the published event.
        """
        envelope = self._ensure_envelope(stream, event)
        fields = envelope.to_stream_fields()

        message_id = await self._client.xadd(stream, fields)
        # Redis returns bytes when decode_responses is False
        if isinstance(message_id, bytes):
            message_id = message_id.decode()

        logger.debug(
            "Event published",
            extra={
                "stream": stream,
                "event_type": envelope.event_type,
                "message_id": message_id,
                "correlation_id": envelope.correlation_id,
            },
        )
        return message_id

    async def publish_event(self, envelope: EventEnvelope) -> str:
        """Publish a fully-formed EventEnvelope to its category stream.

        Args:
            envelope: The event envelope to publish.

        Returns:
            The Redis message ID assigned to the published event.
        """
        stream = STREAM_NAMES.get(envelope.category, f"events:{envelope.category.value}")
        fields = envelope.to_stream_fields()

        message_id = await self._client.xadd(stream, fields)
        if isinstance(message_id, bytes):
            message_id = message_id.decode()

        logger.debug(
            "Event published",
            extra={
                "stream": stream,
                "event_type": envelope.event_type,
                "message_id": message_id,
                "correlation_id": envelope.correlation_id,
            },
        )
        return message_id

    async def create_consumer_group(
        self,
        stream: str,
        group_name: str,
        start_id: str = "0",
    ) -> bool:
        """Create a consumer group for a stream.

        Consumer groups provide isolation — each group independently tracks
        which messages have been delivered and acknowledged. This allows
        multiple components to consume the same stream without interfering.

        Args:
            stream: The stream to create the group on.
            group_name: Unique name for the consumer group.
            start_id: Message ID to start reading from. Use "0" for all
                existing messages, "$" for only new messages.

        Returns:
            True if the group was created, False if it already exists.
        """
        try:
            await self._client.xgroup_create(
                stream, group_name, id=start_id, mkstream=True
            )
            logger.info(
                "Consumer group created",
                extra={"stream": stream, "group_name": group_name},
            )
            return True
        except redis.ResponseError as exc:
            if "BUSYGROUP" in str(exc):
                # Group already exists — not an error
                logger.debug(
                    "Consumer group already exists",
                    extra={"stream": stream, "group_name": group_name},
                )
                return False
            raise

    async def consume(
        self,
        stream: str,
        group_name: str,
        consumer_name: str,
        count: int = 10,
        block_ms: int = 5000,
    ) -> list[ConsumedEvent]:
        """Read events from a stream as part of a consumer group.

        Args:
            stream: The stream to read from.
            group_name: The consumer group name.
            consumer_name: Unique name for this consumer within the group.
            count: Maximum number of messages to read per call.
            block_ms: Milliseconds to block waiting for new messages.
                Use 0 for non-blocking.

        Returns:
            List of consumed events (may be empty if no messages available).
        """
        results = await self._client.xreadgroup(
            groupname=group_name,
            consumername=consumer_name,
            streams={stream: ">"},
            count=count,
            block=block_ms,
        )

        events: list[ConsumedEvent] = []
        if not results:
            return events

        for _stream_name, messages in results:
            for message_id, fields in messages:
                mid = message_id.decode() if isinstance(message_id, bytes) else message_id
                try:
                    envelope = EventEnvelope.from_stream_fields(fields)
                    events.append(ConsumedEvent(message_id=mid, envelope=envelope))
                except (KeyError, ValueError, json.JSONDecodeError) as exc:
                    logger.warning(
                        "Failed to parse event from stream",
                        extra={
                            "stream": stream,
                            "message_id": mid,
                            "error": str(exc),
                        },
                    )

        return events

    async def acknowledge(
        self,
        stream: str,
        group_name: str,
        *message_ids: str,
    ) -> int:
        """Acknowledge processed messages in a consumer group.

        Acknowledged messages will not be re-delivered to any consumer
        in the group.

        Args:
            stream: The stream the messages belong to.
            group_name: The consumer group name.
            *message_ids: One or more message IDs to acknowledge.

        Returns:
            Number of messages successfully acknowledged.
        """
        if not message_ids:
            return 0
        count = await self._client.xack(stream, group_name, *message_ids)
        return int(count)

    async def get_pending_count(self, stream: str, group_name: str) -> int:
        """Get the number of pending (unacknowledged) messages for a group.

        Args:
            stream: The stream to check.
            group_name: The consumer group name.

        Returns:
            Number of pending messages, or 0 if the group doesn't exist.
        """
        try:
            info = await self._client.xpending(stream, group_name)
            # xpending returns a dict or list depending on redis-py version
            if isinstance(info, dict):
                return int(info.get("pending", 0))
            # Tuple format: (pending_count, min_id, max_id, consumers)
            if isinstance(info, (list, tuple)) and len(info) >= 1:
                return int(info[0])
            return 0
        except redis.ResponseError:
            return 0

    def _ensure_envelope(self, stream: str, event: dict[str, Any]) -> EventEnvelope:
        """Wrap a raw event dict in an EventEnvelope if needed.

        If the event already contains all envelope fields, it is used
        directly. Otherwise, missing fields are filled with defaults.
        """
        # Determine category from stream name
        category = EventCategory.TASK_LIFECYCLE
        for cat, name in STREAM_NAMES.items():
            if name == stream:
                category = cat
                break

        return EventEnvelope(
            event_type=event.get("event_type", "unknown"),
            source_component=event.get("source_component", "unknown"),
            timestamp=event.get("timestamp", datetime.now(timezone.utc).isoformat()),
            correlation_id=event.get("correlation_id", str(uuid.uuid4())),
            payload=event.get("payload", event),
            severity=EventSeverity(event.get("severity", EventSeverity.INFO.value)),
            category=EventCategory(event.get("category", category.value)),
        )
