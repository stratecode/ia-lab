"""Add research and evaluation persistence tables.

Revision ID: 005
Revises: 004
Create Date: 2026-05-18
"""

from __future__ import annotations

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql


revision = "005"
down_revision = "004"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "research_runs",
        sa.Column("id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("task_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("entrypoint", sa.String(length=32), nullable=False),
        sa.Column("query", sa.Text(), nullable=False),
        sa.Column("intent_type", sa.String(length=32), nullable=False),
        sa.Column("selected_capabilities", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'[]'::jsonb")),
        sa.Column("final_answer", sa.Text(), nullable=False),
        sa.Column("confidence", sa.Float(), nullable=True),
        sa.Column("source_artifact_ids", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'[]'::jsonb")),
        sa.Column("tool_invocation_ids", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'[]'::jsonb")),
        sa.Column("metadata", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'{}'::jsonb")),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.text("now()"), nullable=False),
        sa.ForeignKeyConstraint(["task_id"], ["tasks.id"], ondelete="CASCADE"),
        sa.PrimaryKeyConstraint("id"),
    )
    op.create_index("ix_research_runs_task_id", "research_runs", ["task_id"], unique=False)
    op.create_index(
        "ix_research_runs_entrypoint_created_at",
        "research_runs",
        ["entrypoint", "created_at"],
        unique=False,
    )

    op.create_table(
        "evaluation_runs",
        sa.Column("id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("research_run_id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("reference_provider", sa.String(length=32), nullable=False),
        sa.Column("reference_model", sa.String(length=128), nullable=True),
        sa.Column("reference_answer", sa.Text(), nullable=True),
        sa.Column("judge_model", sa.String(length=128), nullable=True),
        sa.Column("judge_verdict", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'{}'::jsonb")),
        sa.Column("judge_scores", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'{}'::jsonb")),
        sa.Column("winner", sa.String(length=32), nullable=True),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.text("now()"), nullable=False),
        sa.ForeignKeyConstraint(["research_run_id"], ["research_runs.id"], ondelete="CASCADE"),
        sa.PrimaryKeyConstraint("id"),
    )
    op.create_index("ix_evaluation_runs_research_run_id", "evaluation_runs", ["research_run_id"], unique=False)
    op.create_index("ix_evaluation_runs_created_at", "evaluation_runs", ["created_at"], unique=False)

    op.create_table(
        "evaluation_dataset_items",
        sa.Column("id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("research_run_id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("evaluation_run_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("query", sa.Text(), nullable=False),
        sa.Column("orchestrator_answer", sa.Text(), nullable=False),
        sa.Column("reference_answer", sa.Text(), nullable=False),
        sa.Column("sources", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'[]'::jsonb")),
        sa.Column("scores", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'{}'::jsonb")),
        sa.Column("winner", sa.String(length=32), nullable=True),
        sa.Column("metadata", postgresql.JSONB(astext_type=sa.Text()), nullable=True, server_default=sa.text("'{}'::jsonb")),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.text("now()"), nullable=False),
        sa.ForeignKeyConstraint(["evaluation_run_id"], ["evaluation_runs.id"], ondelete="SET NULL"),
        sa.ForeignKeyConstraint(["research_run_id"], ["research_runs.id"], ondelete="CASCADE"),
        sa.PrimaryKeyConstraint("id"),
    )
    op.create_index(
        "ix_evaluation_dataset_items_research_run_id",
        "evaluation_dataset_items",
        ["research_run_id"],
        unique=False,
    )
    op.create_index(
        "ix_evaluation_dataset_items_created_at",
        "evaluation_dataset_items",
        ["created_at"],
        unique=False,
    )


def downgrade() -> None:
    op.drop_index("ix_evaluation_dataset_items_created_at", table_name="evaluation_dataset_items")
    op.drop_index("ix_evaluation_dataset_items_research_run_id", table_name="evaluation_dataset_items")
    op.drop_table("evaluation_dataset_items")

    op.drop_index("ix_evaluation_runs_created_at", table_name="evaluation_runs")
    op.drop_index("ix_evaluation_runs_research_run_id", table_name="evaluation_runs")
    op.drop_table("evaluation_runs")

    op.drop_index("ix_research_runs_entrypoint_created_at", table_name="research_runs")
    op.drop_index("ix_research_runs_task_id", table_name="research_runs")
    op.drop_table("research_runs")
