"""Add task archiving support.

Revision ID: 006
Revises: 005
Create Date: 2026-05-18
"""

from __future__ import annotations

from alembic import op
import sqlalchemy as sa


revision = "006"
down_revision = "005"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column("tasks", sa.Column("archived_at", sa.DateTime(timezone=True), nullable=True))
    op.create_index("ix_tasks_archived_at", "tasks", ["archived_at"], unique=False)


def downgrade() -> None:
    op.drop_index("ix_tasks_archived_at", table_name="tasks")
    op.drop_column("tasks", "archived_at")
