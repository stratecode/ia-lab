"""Data access repositories.

Exports all repository classes for convenient imports:
    from orchestrator.persistence.repositories import TaskRepository, ...
"""

from orchestrator.persistence.repositories.api_keys import ApiKeyRepository
from orchestrator.persistence.repositories.approvals import ApprovalRepository
from orchestrator.persistence.repositories.audit import AuditRepository
from orchestrator.persistence.repositories.tasks import TaskRepository

__all__ = [
    "ApiKeyRepository",
    "ApprovalRepository",
    "AuditRepository",
    "TaskRepository",
]
