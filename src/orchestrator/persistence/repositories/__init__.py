"""Data access repositories.

Exports all repository classes for convenient imports:
    from orchestrator.persistence.repositories import TaskRepository, ...
"""

from orchestrator.persistence.repositories.api_keys import ApiKeyRepository
from orchestrator.persistence.repositories.approvals import ApprovalRepository
from orchestrator.persistence.repositories.artifacts import ArtifactRepository
from orchestrator.persistence.repositories.audit import AuditRepository
from orchestrator.persistence.repositories.local_bridges import LocalBridgeRepository
from orchestrator.persistence.repositories.tasks import TaskRepository
from orchestrator.persistence.repositories.tool_invocations import ToolInvocationRepository

__all__ = [
    "ApiKeyRepository",
    "ApprovalRepository",
    "ArtifactRepository",
    "AuditRepository",
    "LocalBridgeRepository",
    "TaskRepository",
    "ToolInvocationRepository",
]
