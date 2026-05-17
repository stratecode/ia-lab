"""Safe mode enforcer for blocking destructive operations.

Implements the Safe_Mode toggle that blocks destructive tool operations
when enabled, while allowing read operations and local writes within
workspaces. Emits security.safe_mode_blocked events on blocked operations.

Supports runtime toggle via API (admin scope) without restart.

Requirements: 11.1, 11.2, 11.3, 11.4, 11.5, 11.6
"""

from __future__ import annotations

import logging
import re
import threading
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from enum import StrEnum
from typing import Any, Protocol

logger = logging.getLogger(__name__)


class OperationVerdict(StrEnum):
    """Result of safe mode check."""

    ALLOWED = "allowed"
    BLOCKED = "blocked"


@dataclass(frozen=True)
class SafeModeCheckResult:
    """Result of checking an operation against safe mode rules.

    Attributes:
        verdict: Whether the operation is allowed or blocked.
        reason: Human-readable explanation of the decision.
        rule: The rule that matched (if blocked).
        operation: The original operation string.
    """

    verdict: OperationVerdict
    reason: str
    rule: str | None = None
    operation: str = ""


class IEventPublisher(Protocol):
    """Protocol for event publishing (satisfied by RedisEventBus)."""

    async def publish(self, stream: str, event: dict[str, Any]) -> str: ...


# ---------------------------------------------------------------------------
# Blocking rules — patterns that identify destructive operations
# ---------------------------------------------------------------------------

# Git push operations (any remote push)
_GIT_PUSH_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"\bgit\s+push\b", re.IGNORECASE),
    re.compile(r"\bgit\s+push\s+", re.IGNORECASE),
]

# File deletion outside workspace (exclude docker rm via negative lookbehind)
_FILE_DELETE_COMMANDS: list[re.Pattern[str]] = [
    re.compile(r"(?<!docker )\brm\s+(-[a-zA-Z]*\s+)*", re.IGNORECASE),
    re.compile(r"\bunlink\s+", re.IGNORECASE),
    re.compile(r"\brmdir\s+", re.IGNORECASE),
    re.compile(r"\bshred\s+", re.IGNORECASE),
]

# External API mutations (POST/PUT/DELETE to non-localhost URLs)
_EXTERNAL_API_PATTERNS: list[re.Pattern[str]] = [
    re.compile(
        r"\bcurl\s+.*-X\s+(POST|PUT|DELETE|PATCH)\s+.*https?://(?!localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])",
        re.IGNORECASE,
    ),
    re.compile(
        r"\bcurl\s+.*--request\s+(POST|PUT|DELETE|PATCH)\s+.*https?://(?!localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])",
        re.IGNORECASE,
    ),
    re.compile(
        r"\bcurl\s+.*-d\s+.*https?://(?!localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])",
        re.IGNORECASE,
    ),
    re.compile(
        r"\bhttpx?\.(post|put|delete|patch)\s*\(\s*[\"']https?://(?!localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])",
        re.IGNORECASE,
    ),
    re.compile(
        r"\brequests\.(post|put|delete|patch)\s*\(\s*[\"']https?://(?!localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])",
        re.IGNORECASE,
    ),
]

# Infrastructure changes (ansible, terraform, etc.)
_INFRA_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"\bansible-playbook\b", re.IGNORECASE),
    re.compile(r"\bansible\b", re.IGNORECASE),
    re.compile(r"\bterraform\s+(apply|destroy|import)\b", re.IGNORECASE),
    re.compile(r"\bpulumi\s+(up|destroy)\b", re.IGNORECASE),
    re.compile(r"\bkubectl\s+(apply|delete|create|replace|patch)\b", re.IGNORECASE),
    re.compile(r"\bhelm\s+(install|upgrade|uninstall|delete)\b", re.IGNORECASE),
]

# Docker container lifecycle operations
_DOCKER_LIFECYCLE_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"\bdocker\s+run\b", re.IGNORECASE),
    re.compile(r"\bdocker\s+stop\b", re.IGNORECASE),
    re.compile(r"\bdocker\s+rm\b", re.IGNORECASE),
    re.compile(r"\bdocker\s+kill\b", re.IGNORECASE),
    re.compile(r"\bdocker\s+start\b", re.IGNORECASE),
    re.compile(r"\bdocker\s+restart\b", re.IGNORECASE),
    re.compile(r"\bdocker\s+compose\s+(up|down|stop|rm|restart|kill)\b", re.IGNORECASE),
    re.compile(r"\bdocker-compose\s+(up|down|stop|rm|restart|kill)\b", re.IGNORECASE),
]

# ---------------------------------------------------------------------------
# Allow rules — patterns that identify safe operations
# ---------------------------------------------------------------------------

# Read operations (always allowed)
_READ_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"\bgit\s+(status|log|diff|show|branch|remote|fetch)\b", re.IGNORECASE),
    re.compile(r"\bcat\b", re.IGNORECASE),
    re.compile(r"\bls\b", re.IGNORECASE),
    re.compile(r"\bfind\b", re.IGNORECASE),
    re.compile(r"\bgrep\b", re.IGNORECASE),
    re.compile(r"\bhead\b", re.IGNORECASE),
    re.compile(r"\btail\b", re.IGNORECASE),
    re.compile(r"\bwc\b", re.IGNORECASE),
    re.compile(r"\bfile\b", re.IGNORECASE),
    re.compile(r"\bstat\b", re.IGNORECASE),
    re.compile(r"\bread\b", re.IGNORECASE),
]

# Git commits (without push) — always allowed
_GIT_COMMIT_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"\bgit\s+commit\b", re.IGNORECASE),
    re.compile(r"\bgit\s+add\b", re.IGNORECASE),
    re.compile(r"\bgit\s+stash\b", re.IGNORECASE),
    re.compile(r"\bgit\s+checkout\b", re.IGNORECASE),
    re.compile(r"\bgit\s+switch\b", re.IGNORECASE),
    re.compile(r"\bgit\s+merge\b", re.IGNORECASE),
    re.compile(r"\bgit\s+rebase\b", re.IGNORECASE),
]

# Local test execution — always allowed
_TEST_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"\bpytest\b", re.IGNORECASE),
    re.compile(r"\bnpm\s+test\b", re.IGNORECASE),
    re.compile(r"\bnpm\s+run\s+test\b", re.IGNORECASE),
    re.compile(r"\byarn\s+test\b", re.IGNORECASE),
    re.compile(r"\bmake\s+test\b", re.IGNORECASE),
    re.compile(r"\bgo\s+test\b", re.IGNORECASE),
    re.compile(r"\bcargo\s+test\b", re.IGNORECASE),
    re.compile(r"\bunittest\b", re.IGNORECASE),
    re.compile(r"\bnose2?\b", re.IGNORECASE),
    re.compile(r"\btox\b", re.IGNORECASE),
]


# ---------------------------------------------------------------------------
# Rule categories for reporting
# ---------------------------------------------------------------------------

_BLOCK_RULES: dict[str, list[re.Pattern[str]]] = {
    "git_push": _GIT_PUSH_PATTERNS,
    "docker_lifecycle": _DOCKER_LIFECYCLE_PATTERNS,
    "file_deletion_outside_workspace": _FILE_DELETE_COMMANDS,
    "external_api_mutation": _EXTERNAL_API_PATTERNS,
    "infrastructure_change": _INFRA_PATTERNS,
}

_ALLOW_RULES: dict[str, list[re.Pattern[str]]] = {
    "read_operation": _READ_PATTERNS,
    "git_commit": _GIT_COMMIT_PATTERNS,
    "test_execution": _TEST_PATTERNS,
}


class SafeModeEnforcer:
    """Enforces safe mode by blocking destructive operations.

    Thread-safe: the enabled flag can be toggled at runtime from any thread
    (e.g., via an API endpoint) without requiring a restart.

    Usage:
        enforcer = SafeModeEnforcer(enabled=True, event_bus=bus)
        result = await enforcer.check_operation("git push origin main", "/workspace/task-123")
        if result.verdict == OperationVerdict.BLOCKED:
            # Operation was blocked
            ...
    """

    def __init__(
        self,
        enabled: bool = True,
        event_bus: IEventPublisher | None = None,
    ) -> None:
        """Initialize the safe mode enforcer.

        Args:
            enabled: Whether safe mode is initially enabled.
            event_bus: Optional event publisher for emitting blocked events.
        """
        self._lock = threading.Lock()
        self._enabled = enabled
        self._event_bus = event_bus

    @property
    def enabled(self) -> bool:
        """Whether safe mode is currently enabled (thread-safe read)."""
        with self._lock:
            return self._enabled

    @enabled.setter
    def enabled(self, value: bool) -> None:
        """Toggle safe mode on/off (thread-safe write)."""
        with self._lock:
            old_value = self._enabled
            self._enabled = value
        if old_value != value:
            logger.info(
                "Safe mode toggled",
                extra={"safe_mode_enabled": value, "previous": old_value},
            )

    async def check_operation(
        self,
        operation: str,
        workspace_path: str | None = None,
    ) -> SafeModeCheckResult:
        """Check whether an operation is allowed under current safe mode state.

        Args:
            operation: The command or operation description to check.
            workspace_path: The workspace directory for the current task.
                Used to determine if file operations are within workspace.

        Returns:
            SafeModeCheckResult indicating whether the operation is allowed
            or blocked, with a reason and matched rule.
        """
        # If safe mode is disabled, everything is allowed
        if not self.enabled:
            return SafeModeCheckResult(
                verdict=OperationVerdict.ALLOWED,
                reason="Safe mode is disabled",
                operation=operation,
            )

        # Check explicit allow rules first (reads, commits, tests)
        allow_match = self._match_allow_rules(operation)
        if allow_match:
            return SafeModeCheckResult(
                verdict=OperationVerdict.ALLOWED,
                reason=f"Operation matches allow rule: {allow_match}",
                rule=allow_match,
                operation=operation,
            )

        # Check if it's a local file write within workspace (allowed)
        if workspace_path and self._is_local_write_in_workspace(operation, workspace_path):
            return SafeModeCheckResult(
                verdict=OperationVerdict.ALLOWED,
                reason="Local file write within workspace",
                rule="local_workspace_write",
                operation=operation,
            )

        # Check block rules
        block_match = self._match_block_rules(operation, workspace_path)
        if block_match:
            result = SafeModeCheckResult(
                verdict=OperationVerdict.BLOCKED,
                reason=f"Operation blocked by safe mode rule: {block_match}",
                rule=block_match,
                operation=operation,
            )
            # Emit security event
            await self._emit_blocked_event(operation, block_match, workspace_path)
            return result

        # Default: allow operations that don't match any block rule
        return SafeModeCheckResult(
            verdict=OperationVerdict.ALLOWED,
            reason="Operation does not match any block rule",
            operation=operation,
        )

    def _match_allow_rules(self, operation: str) -> str | None:
        """Check if operation matches any explicit allow rule.

        Returns the rule name if matched, None otherwise.
        """
        for rule_name, patterns in _ALLOW_RULES.items():
            for pattern in patterns:
                if pattern.search(operation):
                    return rule_name
        return None

    def _match_block_rules(
        self,
        operation: str,
        workspace_path: str | None = None,
    ) -> str | None:
        """Check if operation matches any block rule.

        For file deletion, only blocks if the target is outside workspace.

        Returns the rule name if matched, None otherwise.
        """
        for rule_name, patterns in _BLOCK_RULES.items():
            for pattern in patterns:
                if pattern.search(operation):
                    # Special handling for file deletion: only block outside workspace
                    if rule_name == "file_deletion_outside_workspace":
                        if workspace_path and self._is_deletion_in_workspace(
                            operation, workspace_path
                        ):
                            return None  # Deletion within workspace is allowed
                    return rule_name
        return None

    def _is_local_write_in_workspace(
        self,
        operation: str,
        workspace_path: str,
    ) -> bool:
        """Check if the operation is a local file write within the workspace.

        Heuristic: looks for write-like commands (tee, cp, mv, echo >, etc.)
        where the target path starts with the workspace path.
        """
        # Common write patterns
        write_patterns = [
            re.compile(r"\btee\s+(.+)", re.IGNORECASE),
            re.compile(r"\bcp\s+.*\s+(.+)", re.IGNORECASE),
            re.compile(r"\bmv\s+.*\s+(.+)", re.IGNORECASE),
            re.compile(r">\s*(.+)", re.IGNORECASE),
            re.compile(r">>\s*(.+)", re.IGNORECASE),
        ]
        for pattern in write_patterns:
            match = pattern.search(operation)
            if match:
                target = match.group(1).strip().strip("'\"")
                if target.startswith(workspace_path):
                    return True
        return False

    def _is_deletion_in_workspace(
        self,
        operation: str,
        workspace_path: str,
    ) -> bool:
        """Check if a deletion command targets files within the workspace.

        Returns True if all referenced paths are within the workspace.
        """
        # Extract paths from rm/unlink commands (after flags)
        # Simple heuristic: check if workspace_path appears in the operation
        # after the command and flags
        parts = operation.split()
        if not parts:
            return False

        # Skip command and flags
        paths_start = False
        for part in parts[1:]:
            if part.startswith("-"):
                continue
            paths_start = True
            # Check if this path is within workspace
            resolved = part.strip("'\"")
            if not resolved.startswith(workspace_path):
                return False

        # If we found paths and all are in workspace, allow
        return paths_start

    async def _emit_blocked_event(
        self,
        operation: str,
        rule: str,
        workspace_path: str | None,
    ) -> None:
        """Emit a security.safe_mode_blocked event."""
        if not self._event_bus:
            logger.warning(
                "Safe mode blocked operation (no event bus configured)",
                extra={
                    "operation": operation,
                    "rule": rule,
                    "workspace_path": workspace_path,
                },
            )
            return

        event = {
            "event_type": "security.safe_mode_blocked",
            "source_component": "tools.safe_mode",
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "correlation_id": str(uuid.uuid4()),
            "payload": {
                "operation": operation,
                "rule": rule,
                "workspace_path": workspace_path,
            },
            "severity": "warning",
            "category": "security",
        }

        try:
            await self._event_bus.publish("events:security", event)
            logger.info(
                "Emitted safe_mode_blocked event",
                extra={"operation": operation, "rule": rule},
            )
        except Exception as exc:
            logger.error(
                "Failed to emit safe_mode_blocked event",
                extra={"error": str(exc), "operation": operation},
            )
