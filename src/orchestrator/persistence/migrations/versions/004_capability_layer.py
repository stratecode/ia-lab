"""Add capability layer persistence tables.

Revision ID: 004
Revises: 003
Create Date: 2026-05-17
"""

from __future__ import annotations

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql


revision = "004"
down_revision = "003"
branch_labels = None
depends_on = None


def upgrade() -> None:
    agenttype_enum = postgresql.ENUM(
        "planner", "coder", "reviewer", "infra", "researcher",
        name="agenttype",
        create_type=False,
    )
    op.create_table(
        "tool_invocations",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True, nullable=False),
        sa.Column("task_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("agent_type", agenttype_enum, nullable=True),
        sa.Column("entrypoint", sa.String(length=32), nullable=False),
        sa.Column("capability", sa.String(length=64), nullable=False),
        sa.Column("input_payload", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'{}'::jsonb")),
        sa.Column("output_payload", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'{}'::jsonb")),
        sa.Column("status", sa.String(length=32), nullable=False),
        sa.Column("duration_ms", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("source_refs", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'[]'::jsonb")),
        sa.Column("artifact_ids", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'[]'::jsonb")),
        sa.Column("error_message", sa.Text(), nullable=True),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now(), nullable=False),
        sa.ForeignKeyConstraint(["task_id"], ["tasks.id"], ondelete="CASCADE"),
    )
    op.create_index("ix_tool_invocations_task_id", "tool_invocations", ["task_id"])
    op.create_index("ix_tool_invocations_capability", "tool_invocations", ["capability"])
    op.create_index(
        "ix_tool_invocations_entrypoint_created_at",
        "tool_invocations",
        ["entrypoint", "created_at"],
    )

    op.create_table(
        "artifacts",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True, nullable=False),
        sa.Column("task_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("invocation_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("artifact_type", sa.String(length=64), nullable=False),
        sa.Column("title", sa.String(length=255), nullable=True),
        sa.Column("uri", sa.String(length=2048), nullable=True),
        sa.Column("media_type", sa.String(length=255), nullable=True),
        sa.Column("content_text", sa.Text(), nullable=True),
        sa.Column("metadata", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'{}'::jsonb")),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now(), nullable=False),
        sa.ForeignKeyConstraint(["task_id"], ["tasks.id"], ondelete="CASCADE"),
        sa.ForeignKeyConstraint(["invocation_id"], ["tool_invocations.id"], ondelete="CASCADE"),
    )
    op.create_index("ix_artifacts_task_id", "artifacts", ["task_id"])
    op.create_index("ix_artifacts_invocation_id", "artifacts", ["invocation_id"])
    op.create_index("ix_artifacts_type_created_at", "artifacts", ["artifact_type", "created_at"])


def downgrade() -> None:
    op.drop_index("ix_artifacts_type_created_at", table_name="artifacts")
    op.drop_index("ix_artifacts_invocation_id", table_name="artifacts")
    op.drop_index("ix_artifacts_task_id", table_name="artifacts")
    op.drop_table("artifacts")

    op.drop_index("ix_tool_invocations_entrypoint_created_at", table_name="tool_invocations")
    op.drop_index("ix_tool_invocations_capability", table_name="tool_invocations")
    op.drop_index("ix_tool_invocations_task_id", table_name="tool_invocations")
    op.drop_table("tool_invocations")
