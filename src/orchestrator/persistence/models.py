"""SQLAlchemy ORM models for the orchestrator persistence layer."""

import uuid

from sqlalchemy import (
    Boolean,
    DateTime,
    Enum,
    ForeignKey,
    Index,
    Integer,
    String,
    Text,
    Float,
    func,
    text,
)
from sqlalchemy.dialects.postgresql import JSONB, UUID
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column, relationship

from orchestrator.state_machine.transitions import (
    AgentType,
    ApiKeyScope,
    ApprovalStatus,
    ExecutionTarget,
    Priority,
    TaskKind,
    TaskState,
    TERMINAL_STATES,
)


class Base(DeclarativeBase):
    """Base class for all ORM models."""

    pass


# Build the SQL expression for non-terminal states used in partial indexes.
_NON_TERMINAL_WHERE = text(
    f"state NOT IN ({', '.join(repr(s.value) for s in TERMINAL_STATES)})"
)


class Task(Base):
    """Represents a unit of work managed by the orchestrator."""

    __tablename__ = "tasks"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    state: Mapped[str] = mapped_column(
        Enum(TaskState, name="taskstate", values_callable=lambda e: [x.value for x in e]),
        nullable=False,
        default=TaskState.CREATED,
    )
    description: Mapped[str] = mapped_column(Text, nullable=False)
    parent_task_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("tasks.id", ondelete="CASCADE"),
        nullable=True,
    )
    root_task_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("tasks.id", ondelete="CASCADE"),
        nullable=True,
    )
    task_kind: Mapped[str] = mapped_column(
        Enum(
            TaskKind,
            name="taskkind",
            values_callable=lambda e: [x.value for x in e],
        ),
        nullable=False,
        default=TaskKind.ROOT,
    )
    metadata_: Mapped[dict | None] = mapped_column(
        "metadata", JSONB, default=dict, nullable=True
    )
    assigned_agent: Mapped[str | None] = mapped_column(
        Enum(AgentType, name="agenttype", values_callable=lambda e: [x.value for x in e]),
        nullable=True,
    )
    priority: Mapped[str] = mapped_column(
        Enum(Priority, name="priority", values_callable=lambda e: [x.value for x in e]),
        nullable=False,
        default=Priority.NORMAL,
    )
    execution_target: Mapped[str] = mapped_column(
        Enum(
            ExecutionTarget,
            name="executiontarget",
            values_callable=lambda e: [x.value for x in e],
        ),
        nullable=False,
        default=ExecutionTarget.REMOTE,
    )
    workspace_path: Mapped[str | None] = mapped_column(
        String(512), nullable=True
    )
    retry_count: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    max_retries: Mapped[int] = mapped_column(Integer, nullable=False, default=3)
    idempotency_key: Mapped[str | None] = mapped_column(
        String(128), nullable=True
    )
    correlation_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), nullable=False, default=uuid.uuid4
    )
    results: Mapped[dict | None] = mapped_column(JSONB, nullable=True)
    error_message: Mapped[str | None] = mapped_column(Text, nullable=True)

    created_at: Mapped[str] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), nullable=False
    )
    updated_at: Mapped[str] = mapped_column(
        DateTime(timezone=True),
        server_default=func.now(),
        onupdate=func.now(),
        nullable=False,
    )
    started_at: Mapped[str | None] = mapped_column(
        DateTime(timezone=True), nullable=True
    )
    completed_at: Mapped[str | None] = mapped_column(
        DateTime(timezone=True), nullable=True
    )
    archived_at: Mapped[str | None] = mapped_column(
        DateTime(timezone=True), nullable=True
    )
    queued_at: Mapped[str | None] = mapped_column(
        DateTime(timezone=True), nullable=True
    )

    # Relationships
    transitions: Mapped[list["StateTransition"]] = relationship(
        "StateTransition", back_populates="task", cascade="all, delete-orphan"
    )
    approvals: Mapped[list["Approval"]] = relationship(
        "Approval", back_populates="task", cascade="all, delete-orphan"
    )
    tool_invocations: Mapped[list["ToolInvocation"]] = relationship(
        "ToolInvocation", back_populates="task", cascade="all, delete-orphan"
    )
    artifacts: Mapped[list["Artifact"]] = relationship(
        "Artifact", back_populates="task", cascade="all, delete-orphan"
    )
    parent_task: Mapped["Task | None"] = relationship(
        "Task",
        remote_side="Task.id",
        foreign_keys=[parent_task_id],
        back_populates="child_tasks",
    )
    child_tasks: Mapped[list["Task"]] = relationship(
        "Task",
        back_populates="parent_task",
        foreign_keys=[parent_task_id],
        cascade="all, delete-orphan",
    )
    root_task: Mapped["Task | None"] = relationship(
        "Task",
        remote_side="Task.id",
        foreign_keys=[root_task_id],
    )

    __table_args__ = (
        Index("ix_tasks_state_priority", "state", "priority"),
        Index("ix_tasks_created_at", "created_at"),
        Index("ix_tasks_parent_task_id", "parent_task_id"),
        Index("ix_tasks_root_task_id", "root_task_id"),
        Index("ix_tasks_execution_target_state", "execution_target", "state"),
        # Partial unique index: idempotency_key must be unique when not NULL
        Index(
            "ix_tasks_idempotency_key",
            "idempotency_key",
            unique=True,
            postgresql_where=text("idempotency_key IS NOT NULL"),
        ),
        # Partial unique index: workspace_path must be unique for non-terminal tasks
        Index(
            "ix_tasks_workspace_path_active",
            "workspace_path",
            unique=True,
            postgresql_where=_NON_TERMINAL_WHERE,
        ),
    )


class StateTransition(Base):
    """Records each state transition for audit and debugging."""

    __tablename__ = "state_transitions"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    task_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("tasks.id", ondelete="CASCADE"),
        nullable=False,
    )
    from_state: Mapped[str] = mapped_column(
        Enum(TaskState, name="taskstate", values_callable=lambda e: [x.value for x in e]),
        nullable=False,
    )
    to_state: Mapped[str] = mapped_column(
        Enum(TaskState, name="taskstate", values_callable=lambda e: [x.value for x in e]),
        nullable=False,
    )
    actor: Mapped[str] = mapped_column(String(128), nullable=False)
    reason: Mapped[str | None] = mapped_column(Text, nullable=True)
    timestamp: Mapped[str] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), nullable=False
    )

    # Relationship
    task: Mapped["Task"] = relationship("Task", back_populates="transitions")

    __table_args__ = (
        Index("ix_state_transitions_task_id_timestamp", "task_id", "timestamp"),
    )


class Approval(Base):
    """Tracks approval requests for high-risk operations."""

    __tablename__ = "approvals"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    task_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("tasks.id", ondelete="CASCADE"),
        nullable=False,
    )
    action_type: Mapped[str] = mapped_column(String(64), nullable=False)
    target_resource: Mapped[str] = mapped_column(String(512), nullable=False)
    status: Mapped[str] = mapped_column(
        Enum(
            ApprovalStatus,
            name="approvalstatus",
            values_callable=lambda e: [x.value for x in e],
        ),
        nullable=False,
        default=ApprovalStatus.PENDING,
    )
    operator: Mapped[str | None] = mapped_column(String(128), nullable=True)
    timeout_seconds: Mapped[int] = mapped_column(
        Integer, nullable=False, default=300
    )
    escalation_level: Mapped[int] = mapped_column(
        Integer, nullable=False, default=1
    )

    requested_at: Mapped[str] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), nullable=False
    )
    resolved_at: Mapped[str | None] = mapped_column(
        DateTime(timezone=True), nullable=True
    )
    timeout_at: Mapped[str] = mapped_column(
        DateTime(timezone=True), nullable=False
    )

    # Relationship
    task: Mapped["Task"] = relationship("Task", back_populates="approvals")

    __table_args__ = (
        Index("ix_approvals_status", "status"),
        Index(
            "ix_approvals_timeout_at",
            "timeout_at",
            postgresql_where=text("status = 'pending'"),
        ),
    )


class AuditLog(Base):
    """Immutable audit trail for all state-changing operations."""

    __tablename__ = "audit_log"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    timestamp: Mapped[str] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), nullable=False
    )
    actor: Mapped[str] = mapped_column(String(128), nullable=False)
    action: Mapped[str] = mapped_column(String(64), nullable=False)
    resource_type: Mapped[str] = mapped_column(String(64), nullable=False)
    resource_id: Mapped[str | None] = mapped_column(String(128), nullable=True)
    details: Mapped[dict | None] = mapped_column(JSONB, default=dict, nullable=True)
    correlation_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), nullable=False
    )

    __table_args__ = (
        Index("ix_audit_log_timestamp", "timestamp"),
        Index("ix_audit_log_actor_action", "actor", "action"),
        Index("ix_audit_log_resource", "resource_type", "resource_id"),
    )


class ToolInvocation(Base):
    """Audit record for a single capability/tool invocation."""

    __tablename__ = "tool_invocations"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    task_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("tasks.id", ondelete="CASCADE"),
        nullable=True,
    )
    agent_type: Mapped[str | None] = mapped_column(
        Enum(AgentType, name="agenttype", values_callable=lambda e: [x.value for x in e]),
        nullable=True,
    )
    entrypoint: Mapped[str] = mapped_column(String(32), nullable=False)
    capability: Mapped[str] = mapped_column(String(64), nullable=False)
    input_payload: Mapped[dict | None] = mapped_column(JSONB, default=dict, nullable=True)
    output_payload: Mapped[dict | None] = mapped_column(JSONB, default=dict, nullable=True)
    status: Mapped[str] = mapped_column(String(32), nullable=False)
    duration_ms: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    source_refs: Mapped[list | None] = mapped_column(JSONB, default=list, nullable=True)
    artifact_ids: Mapped[list | None] = mapped_column(JSONB, default=list, nullable=True)
    error_message: Mapped[str | None] = mapped_column(Text, nullable=True)
    created_at: Mapped[str] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), nullable=False
    )

    task: Mapped["Task | None"] = relationship("Task", back_populates="tool_invocations")
    artifacts: Mapped[list["Artifact"]] = relationship(
        "Artifact", back_populates="invocation", cascade="all, delete-orphan"
    )

    __table_args__ = (
        Index("ix_tool_invocations_task_id", "task_id"),
        Index("ix_tool_invocations_capability", "capability"),
        Index("ix_tool_invocations_entrypoint_created_at", "entrypoint", "created_at"),
    )


class Artifact(Base):
    """Stored artifact emitted by tools or capability invocations."""

    __tablename__ = "artifacts"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    task_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("tasks.id", ondelete="CASCADE"),
        nullable=True,
    )
    invocation_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("tool_invocations.id", ondelete="CASCADE"),
        nullable=True,
    )
    artifact_type: Mapped[str] = mapped_column(String(64), nullable=False)
    title: Mapped[str | None] = mapped_column(String(255), nullable=True)
    uri: Mapped[str | None] = mapped_column(String(2048), nullable=True)
    media_type: Mapped[str | None] = mapped_column(String(255), nullable=True)
    content_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    metadata_: Mapped[dict | None] = mapped_column("metadata", JSONB, default=dict, nullable=True)
    created_at: Mapped[str] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), nullable=False
    )

    task: Mapped["Task | None"] = relationship("Task", back_populates="artifacts")
    invocation: Mapped["ToolInvocation | None"] = relationship(
        "ToolInvocation", back_populates="artifacts"
    )

    __table_args__ = (
        Index("ix_artifacts_task_id", "task_id"),
        Index("ix_artifacts_invocation_id", "invocation_id"),
        Index("ix_artifacts_type_created_at", "artifact_type", "created_at"),
    )


class ResearchRun(Base):
    """Persisted research orchestration runs."""

    __tablename__ = "research_runs"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    task_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("tasks.id", ondelete="CASCADE"),
        nullable=True,
    )
    entrypoint: Mapped[str] = mapped_column(String(32), nullable=False)
    query: Mapped[str] = mapped_column(Text, nullable=False)
    intent_type: Mapped[str] = mapped_column(String(32), nullable=False)
    selected_capabilities: Mapped[list | None] = mapped_column(
        JSONB, default=list, nullable=True
    )
    final_answer: Mapped[str] = mapped_column(Text, nullable=False)
    confidence: Mapped[float | None] = mapped_column(Float, nullable=True)
    source_artifact_ids: Mapped[list | None] = mapped_column(
        JSONB, default=list, nullable=True
    )
    tool_invocation_ids: Mapped[list | None] = mapped_column(
        JSONB, default=list, nullable=True
    )
    metadata_: Mapped[dict | None] = mapped_column(
        "metadata", JSONB, default=dict, nullable=True
    )
    created_at: Mapped[str] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), nullable=False
    )

    task: Mapped["Task | None"] = relationship("Task")

    __table_args__ = (
        Index("ix_research_runs_task_id", "task_id"),
        Index("ix_research_runs_entrypoint_created_at", "entrypoint", "created_at"),
    )


class EvaluationRun(Base):
    """Persisted evaluation run for a research answer."""

    __tablename__ = "evaluation_runs"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    research_run_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("research_runs.id", ondelete="CASCADE"),
        nullable=False,
    )
    reference_provider: Mapped[str] = mapped_column(String(32), nullable=False)
    reference_model: Mapped[str | None] = mapped_column(String(128), nullable=True)
    reference_answer: Mapped[str | None] = mapped_column(Text, nullable=True)
    judge_model: Mapped[str | None] = mapped_column(String(128), nullable=True)
    judge_verdict: Mapped[dict | None] = mapped_column(JSONB, default=dict, nullable=True)
    judge_scores: Mapped[dict | None] = mapped_column(JSONB, default=dict, nullable=True)
    winner: Mapped[str | None] = mapped_column(String(32), nullable=True)
    created_at: Mapped[str] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), nullable=False
    )

    research_run: Mapped["ResearchRun"] = relationship("ResearchRun")

    __table_args__ = (
        Index("ix_evaluation_runs_research_run_id", "research_run_id"),
        Index("ix_evaluation_runs_created_at", "created_at"),
    )


class EvaluationDatasetItem(Base):
    """Prepared dataset item for later tuning/fine-tuning work."""

    __tablename__ = "evaluation_dataset_items"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    research_run_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("research_runs.id", ondelete="CASCADE"),
        nullable=False,
    )
    evaluation_run_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("evaluation_runs.id", ondelete="SET NULL"),
        nullable=True,
    )
    query: Mapped[str] = mapped_column(Text, nullable=False)
    orchestrator_answer: Mapped[str] = mapped_column(Text, nullable=False)
    reference_answer: Mapped[str] = mapped_column(Text, nullable=False)
    sources: Mapped[list | None] = mapped_column(JSONB, default=list, nullable=True)
    scores: Mapped[dict | None] = mapped_column(JSONB, default=dict, nullable=True)
    winner: Mapped[str | None] = mapped_column(String(32), nullable=True)
    metadata_: Mapped[dict | None] = mapped_column(
        "metadata", JSONB, default=dict, nullable=True
    )
    created_at: Mapped[str] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), nullable=False
    )

    research_run: Mapped["ResearchRun"] = relationship("ResearchRun")
    evaluation_run: Mapped["EvaluationRun | None"] = relationship("EvaluationRun")

    __table_args__ = (
        Index("ix_evaluation_dataset_items_research_run_id", "research_run_id"),
        Index("ix_evaluation_dataset_items_created_at", "created_at"),
    )


class LocalBridge(Base):
    """Registered local bridge worker bound to a single workspace root."""

    __tablename__ = "local_bridges"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    name: Mapped[str] = mapped_column(String(128), nullable=False)
    hostname: Mapped[str] = mapped_column(String(255), nullable=False)
    workspace_root: Mapped[str] = mapped_column(String(1024), nullable=False)
    status: Mapped[str] = mapped_column(String(32), nullable=False, default="active")
    capabilities: Mapped[dict | None] = mapped_column(JSONB, default=dict, nullable=True)
    api_key_name: Mapped[str | None] = mapped_column(String(255), nullable=True)
    last_heartbeat: Mapped[str | None] = mapped_column(
        DateTime(timezone=True), nullable=True
    )
    created_at: Mapped[str] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), nullable=False
    )
    updated_at: Mapped[str] = mapped_column(
        DateTime(timezone=True),
        server_default=func.now(),
        onupdate=func.now(),
        nullable=False,
    )

    __table_args__ = (
        Index("ix_local_bridges_workspace_root", "workspace_root"),
        Index("ix_local_bridges_last_heartbeat", "last_heartbeat"),
    )


class ApiKey(Base):
    """API key records for authentication and authorization."""

    __tablename__ = "api_keys"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, default=uuid.uuid4
    )
    key_hash: Mapped[str] = mapped_column(
        String(128), nullable=False, unique=True
    )
    name: Mapped[str] = mapped_column(String(128), nullable=False)
    scope: Mapped[str] = mapped_column(
        Enum(
            ApiKeyScope,
            name="apikeyscope",
            values_callable=lambda e: [x.value for x in e],
        ),
        nullable=False,
    )
    is_active: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    created_at: Mapped[str] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), nullable=False
    )
    last_used_at: Mapped[str | None] = mapped_column(
        DateTime(timezone=True), nullable=True
    )
    revoked_at: Mapped[str | None] = mapped_column(
        DateTime(timezone=True), nullable=True
    )

    __table_args__ = (
        Index("ix_api_keys_active", "is_active", "key_hash"),
    )
