"""Approvals module - approval gate management and timeout watching.

Provides:
- ApprovalManager: Full approval lifecycle (request, resolve, escalation)
- ApprovalDecision: Enum for approve/reject decisions
- IApprovalService: Protocol for approval service consumers
"""

from orchestrator.approvals.interfaces import ApprovalDecision, IApprovalService
from orchestrator.approvals.manager import (
    ApprovalAlreadyResolvedError,
    ApprovalManager,
    ApprovalNotFoundError,
)

__all__ = [
    "ApprovalDecision",
    "ApprovalManager",
    "ApprovalNotFoundError",
    "ApprovalAlreadyResolvedError",
    "IApprovalService",
]
