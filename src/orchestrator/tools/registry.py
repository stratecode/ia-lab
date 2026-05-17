"""Tool registry — manages tool registration and metadata lookup.

Provides a central registry for all tools available to the orchestrator.
Each tool is registered with its name, description, agent types that can
use it, and whether it requires safe mode checks.

Requirements: 17.1, 17.2
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field

from orchestrator.state_machine.transitions import AgentType

logger = logging.getLogger(__name__)

# Maximum tool name length (matches ToolResult.tool_name constraint)
MAX_TOOL_NAME_LENGTH = 64


@dataclass(frozen=True)
class ToolMetadata:
    """Metadata describing a registered tool.

    Attributes:
        name: Unique tool identifier (max 64 chars).
        description: Human-readable description of the tool.
        agent_types: Agent types allowed to invoke this tool.
        requires_safe_mode_check: Whether invocations must pass safe mode.
        timeout_seconds: Default execution timeout for this tool.
    """

    name: str
    description: str
    agent_types: frozenset[AgentType] = field(
        default_factory=lambda: frozenset(AgentType)
    )
    requires_safe_mode_check: bool = True
    timeout_seconds: int = 1800


class ToolRegistryError(Exception):
    """Raised when a tool registry operation fails."""

    pass


class ToolRegistry:
    """Central registry for tool metadata.

    Tools must be registered before they can be invoked by workers.
    The registry validates tool names and prevents duplicate registrations.
    """

    def __init__(self) -> None:
        self._tools: dict[str, ToolMetadata] = {}

    def register(self, metadata: ToolMetadata) -> None:
        """Register a tool with its metadata.

        Args:
            metadata: The tool's metadata including name and capabilities.

        Raises:
            ToolRegistryError: If the tool name is invalid or already registered.
        """
        name = metadata.name

        if not name or not name.strip():
            raise ToolRegistryError("Tool name must be a non-empty string")

        if len(name) > MAX_TOOL_NAME_LENGTH:
            raise ToolRegistryError(
                f"Tool name exceeds maximum length of {MAX_TOOL_NAME_LENGTH}: "
                f"'{name[:20]}...' ({len(name)} chars)"
            )

        if name in self._tools:
            raise ToolRegistryError(
                f"Tool '{name}' is already registered"
            )

        self._tools[name] = metadata
        logger.info("Tool registered", extra={"tool_name": name})

    def unregister(self, name: str) -> None:
        """Remove a tool from the registry.

        Args:
            name: The tool name to unregister.

        Raises:
            ToolRegistryError: If the tool is not registered.
        """
        if name not in self._tools:
            raise ToolRegistryError(f"Tool '{name}' is not registered")

        del self._tools[name]
        logger.info("Tool unregistered", extra={"tool_name": name})

    def get(self, name: str) -> ToolMetadata | None:
        """Look up a tool by name.

        Args:
            name: The tool name to look up.

        Returns:
            The tool's metadata, or None if not registered.
        """
        return self._tools.get(name)

    def is_registered(self, name: str) -> bool:
        """Check if a tool is registered.

        Args:
            name: The tool name to check.

        Returns:
            True if the tool is registered.
        """
        return name in self._tools

    def list_tools(
        self, agent_type: AgentType | None = None
    ) -> list[ToolMetadata]:
        """List all registered tools, optionally filtered by agent type.

        Args:
            agent_type: If provided, only return tools available to this agent.

        Returns:
            List of tool metadata entries.
        """
        if agent_type is None:
            return list(self._tools.values())

        return [
            meta
            for meta in self._tools.values()
            if agent_type in meta.agent_types
        ]

    def get_tool_names(self) -> list[str]:
        """Return all registered tool names.

        Returns:
            Sorted list of tool names.
        """
        return sorted(self._tools.keys())

    @property
    def count(self) -> int:
        """Return the number of registered tools."""
        return len(self._tools)
