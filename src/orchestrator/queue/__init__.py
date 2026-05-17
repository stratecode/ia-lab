"""Queue module - Redis task queue, event bus, and recovery."""

from orchestrator.queue.backpressure import (
    DEAD_LETTER_KEY,
    BackpressureQueueService,
    DeadLetterEntry,
    QueueFullError,
)
from orchestrator.queue.event_bus import (
    ConsumedEvent,
    EventCategory,
    EventEnvelope,
    EventSeverity,
    RedisEventBus,
    STREAM_NAMES,
)
from orchestrator.queue.interfaces import IQueueService
from orchestrator.queue.recovery import (
    EPOCH_KEY,
    ReconciliationResult,
    RedisRecoveryService,
)
from orchestrator.queue.redis_queue import (
    PRIORITY_WEIGHTS,
    RedisQueueService,
    compute_score,
)

__all__ = [
    "BackpressureQueueService",
    "ConsumedEvent",
    "DEAD_LETTER_KEY",
    "DeadLetterEntry",
    "EPOCH_KEY",
    "EventCategory",
    "EventEnvelope",
    "EventSeverity",
    "IQueueService",
    "PRIORITY_WEIGHTS",
    "QueueFullError",
    "ReconciliationResult",
    "RedisEventBus",
    "RedisQueueService",
    "RedisRecoveryService",
    "STREAM_NAMES",
    "compute_score",
]
