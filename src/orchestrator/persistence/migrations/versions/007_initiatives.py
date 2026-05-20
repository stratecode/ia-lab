"""Add initiatives and initiative-linked task metadata.

Revision ID: 007_initiatives
Revises: 006_task_archiving
Create Date: 2026-05-19
"""

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql


revision = "007_initiatives"
down_revision = "006_task_archiving"
branch_labels = None
depends_on = None


def upgrade() -> None:
    agenttype_enum = postgresql.ENUM(
        "planner",
        "coder",
        "reviewer",
        "infra",
        "researcher",
        name="agenttype",
        create_type=False,
    )
    op.add_column("tasks", sa.Column("initiative_id", postgresql.UUID(as_uuid=True), nullable=True))
    op.add_column("tasks", sa.Column("planned_agent", agenttype_enum, nullable=True))
    op.create_index("ix_tasks_initiative_id", "tasks", ["initiative_id"])

    op.create_table(
        "initiatives",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True, nullable=False),
        sa.Column("title", sa.Text(), nullable=False),
        sa.Column("workspace_root", sa.Text(), nullable=False),
        sa.Column("goal", sa.Text(), nullable=False),
        sa.Column("status", sa.String(length=64), nullable=False),
        sa.Column("current_phase", sa.String(length=64), nullable=False),
        sa.Column("active_requirements_artifact_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("active_design_artifact_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("active_plan_artifact_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("created_by", sa.String(length=128), nullable=False),
        sa.Column("execution_mode", sa.String(length=32), nullable=False),
        sa.Column("archived_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now(), nullable=False),
        sa.Column("updated_at", sa.DateTime(timezone=True), server_default=sa.func.now(), nullable=False),
    )
    op.create_index("ix_initiatives_workspace_root", "initiatives", ["workspace_root"])
    op.create_index("ix_initiatives_status_phase", "initiatives", ["status", "current_phase"])

    op.create_table(
        "initiative_phase_reviews",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True, nullable=False),
        sa.Column("initiative_id", postgresql.UUID(as_uuid=True), sa.ForeignKey("initiatives.id", ondelete="CASCADE"), nullable=False),
        sa.Column("phase", sa.String(length=32), nullable=False),
        sa.Column("decision", sa.String(length=32), nullable=False),
        sa.Column("feedback", sa.Text(), nullable=True),
        sa.Column("generated_by", sa.String(length=128), nullable=True),
        sa.Column("artifact_markdown_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("artifact_json_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now(), nullable=False),
    )
    op.create_index("ix_initiative_phase_reviews_initiative_phase", "initiative_phase_reviews", ["initiative_id", "phase", "created_at"])

    op.create_table(
        "initiative_task_links",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True, nullable=False),
        sa.Column("initiative_id", postgresql.UUID(as_uuid=True), sa.ForeignKey("initiatives.id", ondelete="CASCADE"), nullable=False),
        sa.Column("task_id", postgresql.UUID(as_uuid=True), sa.ForeignKey("tasks.id", ondelete="CASCADE"), nullable=False),
        sa.Column("phase_origin", sa.String(length=32), nullable=False),
        sa.Column("epic", sa.Text(), nullable=True),
        sa.Column("launch_group", sa.Text(), nullable=True),
        sa.Column("execution_mode", sa.String(length=32), nullable=False),
        sa.Column("launch_order", sa.Integer(), nullable=False, server_default="10"),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now(), nullable=False),
        sa.UniqueConstraint("initiative_id", "task_id", name="uq_initiative_task_links"),
    )
    op.create_index("ix_initiative_task_links_initiative_launch_order", "initiative_task_links", ["initiative_id", "launch_order"])


def downgrade() -> None:
    op.drop_index("ix_initiative_task_links_initiative_launch_order", table_name="initiative_task_links")
    op.drop_table("initiative_task_links")
    op.drop_index("ix_initiative_phase_reviews_initiative_phase", table_name="initiative_phase_reviews")
    op.drop_table("initiative_phase_reviews")
    op.drop_index("ix_initiatives_status_phase", table_name="initiatives")
    op.drop_index("ix_initiatives_workspace_root", table_name="initiatives")
    op.drop_table("initiatives")
    op.drop_index("ix_tasks_initiative_id", table_name="tasks")
    op.drop_column("tasks", "planned_agent")
    op.drop_column("tasks", "initiative_id")
