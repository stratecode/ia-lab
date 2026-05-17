"""Execution module - worker loop, task runner, consumer loop, and aider adapter."""

from orchestrator.execution.aider_adapter import (
    AiderAdapter,
    AiderTaskParams,
    SubprocessResult,
)
from orchestrator.execution.consumer_loop import (
    ConsumerLoop,
    ConsumerLoopConfig,
    TaskResult,
)
from orchestrator.execution.dead_worker_detector import (
    DeadWorkerDetector,
    DeadWorkerInfo,
    RecoveryResult,
)
from orchestrator.execution.interfaces import ITaskRunner, IWorker
from orchestrator.execution.runner import TaskRunner, TaskRunnerError
from orchestrator.execution.worker import Worker

__all__ = [
    "AiderAdapter",
    "AiderTaskParams",
    "ConsumerLoop",
    "ConsumerLoopConfig",
    "DeadWorkerDetector",
    "DeadWorkerInfo",
    "ITaskRunner",
    "IWorker",
    "RecoveryResult",
    "SubprocessResult",
    "TaskResult",
    "TaskRunner",
    "TaskRunnerError",
    "Worker",
]
