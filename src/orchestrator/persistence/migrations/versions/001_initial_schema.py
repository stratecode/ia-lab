"""Initial schema — all tables, indexes, and constraints.

Revision ID: 001
Revises: None
Create Date: 2024-01-01 00:00:00.000000

"""
from typing import Sequence, Union

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import JSONB, UUID

# revision identifiers, used by Alembic.
revision: str = "001"
down_revision: Union[str, None] = None
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    """Create all orchestrator tables with indexes and constraints."""

    # --- PostgreSQL Enums ---
    taskstate_enum = sa.Enum(
        "created", "queued", "assigned", "in_progress",
        "waiting_approval", "review", "retrying",
        "completed", "failed", "cancelled",
        name="taskstate",
    )
    taskstate_enum.create(op.get_bind(), checkfirst=True)

    agenttype_enum = sa.Enum(
        "planner", "coder", "reviewer", "infra", "researcher",
        name="agenttype",
    )
    agenttype_enum.create(op.get_bind(), checkfirst=True)

    priority_enum = sa.Enum(
        "critical", "high", "normal", "low",
        name="priority",
    )
    priority_enum.create(op.get_bind(), checkfirst=True)

    approvalstatus_enum = sa.Enum(
        "pending", "approved", "rejected", "timeout",
        name="approvalstatus",
    )
    approvalstatus_enum.create(op.get_bind(), checkfirst=True)

    apikeyscope_enum = sa.Enum(
        "admin", "operator", "bot", "readonly",
        name="apikeyscope",
    )
    apikeyscope_enum.create(op.get_bind(), checkfirst=True)

    # --- Tasks table ---
    op.create_table(
        "tasks",
        sa.Column("id", UUID(as_uuid=True), primary_key=True),
        sa.Column("state", taskstate_enum, nullable=False, server_default="created"),
        sa.Column("description", sa.Text(), nullable=False),
        sa.Column("metadata", JSONB(), nullable=True),
        sa.Column("assigned_agent", agenttype_enum, nullable=True),
        sa.Column("priority", priority_enum, nullable=False, server_default="normal"),
        sa.Column("workspace_path", sa.String(512), nullable=True),
        sa.Column("retry_count", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("max_retries", sa.Integer(), nullable=False, server_default="3"),
        sa.Column("idempotency_key", sa.String(128), nullable=True),
        sa.Column("correlation_id", UUID(as_uuid=True), nullable=False),
        sa.Column("results", JSONB(), nullable=True),
        sa.Column("error_message", sa.Text(), nullable=True),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=True),
            server_default=sa.func.now(),
            nullable=False,
        ),
        sa.Column(
            "updated_at",
            sa.DateTime(timezone=True),
            server_default=sa.func.now(),
            nullable=False,
        ),
        sa.Column("started_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("completed_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("queued_at", sa.DateTime(timezone=True), nullable=True),
    )

    # Tasks indexes
    op.create_index("ix_tasks_state_priority", "tasks", ["state", "priority"])
    op.create_index("ix_tasks_created_at", "tasks", ["created_at"])
    op.create_index(
        "ix_tasks_idempotency_key",
        "tasks",
        ["idempotency_key"],
        unique=True,
        postgresql_where=sa.text("idempotency_key IS NOT NULL"),
    )
    op.create_index(
        "ix_tasks_workspace_path_active",
        "tasks",
        ["workspace_path"],
        unique=True,
        postgresql_where=sa.text(
            "state NOT IN ('completed', 'failed', 'cancelled')"
        ),
    )

    # --- State Transitions table ---
    op.create_table(
        "state_transitions",
        sa.Column("id", UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "task_id",
            UUID(as_uuid=True),
            sa.ForeignKey("tasks.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("from_state", taskstate_enum, nullable=False),
        sa.Column("to_state", taskstate_enum, nullable=False),
        sa.Column("actor", sa.String(128), nullable=False),
        sa.Column("reason", sa.Text(), nullable=True),
        sa.Column(
            "timestamp",
            sa.DateTime(timezone=True),
            server_default=sa.func.now(),
            nullable=False,
        ),
    )

    op.create_index(
        "ix_state_transitions_task_id_timestamp",
        "state_transitions",
        ["task_id", "timestamp"],
    )

    # --- Approvals table ---
    op.create_table(
        "approvals",
        sa.Column("id", UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "task_id",
            UUID(as_uuid=True),
            sa.ForeignKey("tasks.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("action_type", sa.String(64), nullable=False),
        sa.Column("target_resource", sa.String(512), nullable=False),
        sa.Column(
            "status", approvalstatus_enum, nullable=False, server_default="pending"
        ),
        sa.Column("operator", sa.String(128), nullable=True),
        sa.Column(
            "timeout_seconds", sa.Integer(), nullable=False, server_default="300"
        ),
        sa.Column(
            "escalation_level", sa.Integer(), nullable=False, server_default="1"
        ),
        sa.Column(
            "requested_at",
            sa.DateTime(timezone=True),
            server_default=sa.func.now(),
            nullable=False,
        ),
        sa.Column("resolved_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("timeout_at", sa.DateTime(timezone=True), nullable=False),
    )

    op.create_index("ix_approvals_status", "approvals", ["status"])
    op.create_index(
        "ix_approvals_timeout_at",
        "approvals",
        ["timeout_at"],
        postgresql_where=sa.text("status = 'pending'"),
    )

    # --- Audit Log table ---
    op.create_table(
        "audit_log",
        sa.Column("id", UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "timestamp",
            sa.DateTime(timezone=True),
            server_default=sa.func.now(),
            nullable=False,
        ),
        sa.Column("actor", sa.String(128), nullable=False),
        sa.Column("action", sa.String(64), nullable=False),
        sa.Column("resource_type", sa.String(64), nullable=False),
        sa.Column("resource_id", sa.String(128), nullable=True),
        sa.Column("details", JSONB(), nullable=True),
        sa.Column("correlation_id", UUID(as_uuid=True), nullable=False),
    )

    op.create_index("ix_audit_log_timestamp", "audit_log", ["timestamp"])
    op.create_index("ix_audit_log_actor_action", "audit_log", ["actor", "action"])
    op.create_index(
        "ix_audit_log_resource", "audit_log", ["resource_type", "resource_id"]
    )

    # --- API Keys table ---
    op.create_table(
        "api_keys",
        sa.Column("id", UUID(as_uuid=True), primary_key=True),
        sa.Column("key_hash", sa.String(128), nullable=False, unique=True),
        sa.Column("name", sa.String(128), nullable=False),
        sa.Column("scope", apikeyscope_enum, nullable=False),
        sa.Column(
            "is_active", sa.Boolean(), nullable=False, server_default=sa.text("true")
        ),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=True),
            server_default=sa.func.now(),
            nullable=False,
        ),
        sa.Column("last_used_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("revoked_at", sa.DateTime(timezone=True), nullable=True),
    )

    op.create_index("ix_api_keys_active", "api_keys", ["is_active", "key_hash"])


def downgrade() -> None:
    """Drop all orchestrator tables and enums."""
    op.drop_table("api_keys")
    op.drop_table("audit_log")
    op.drop_table("approvals")
    op.drop_table("state_transitions")
    op.drop_table("tasks")

    # Drop enums
    sa.Enum(name="apikeyscope").drop(op.get_bind(), checkfirst=True)
    sa.Enum(name="approvalstatus").drop(op.get_bind(), checkfirst=True)
    sa.Enum(name="priority").drop(op.get_bind(), checkfirst=True)
    sa.Enum(name="agenttype").drop(op.get_bind(), checkfirst=True)
    sa.Enum(name="taskstate").drop(op.get_bind(), checkfirst=True)
