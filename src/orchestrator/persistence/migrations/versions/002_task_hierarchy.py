"""Add task hierarchy fields for Phase 5A.

Revision ID: 002
Revises: 001
Create Date: 2026-05-17 00:00:00.000000
"""

from typing import Sequence, Union

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import UUID


revision: str = "002"
down_revision: Union[str, None] = "001"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    taskkind_enum = sa.Enum("root", "plan_step", name="taskkind")
    taskkind_enum.create(op.get_bind(), checkfirst=True)

    op.add_column("tasks", sa.Column("parent_task_id", UUID(as_uuid=True), nullable=True))
    op.add_column("tasks", sa.Column("root_task_id", UUID(as_uuid=True), nullable=True))
    op.add_column(
        "tasks",
        sa.Column("task_kind", taskkind_enum, nullable=False, server_default="root"),
    )

    op.create_foreign_key(
        "fk_tasks_parent_task_id_tasks",
        "tasks",
        "tasks",
        ["parent_task_id"],
        ["id"],
        ondelete="CASCADE",
    )
    op.create_foreign_key(
        "fk_tasks_root_task_id_tasks",
        "tasks",
        "tasks",
        ["root_task_id"],
        ["id"],
        ondelete="CASCADE",
    )
    op.create_index("ix_tasks_parent_task_id", "tasks", ["parent_task_id"])
    op.create_index("ix_tasks_root_task_id", "tasks", ["root_task_id"])

    op.execute("UPDATE tasks SET root_task_id = id WHERE root_task_id IS NULL")


def downgrade() -> None:
    op.drop_index("ix_tasks_root_task_id", table_name="tasks")
    op.drop_index("ix_tasks_parent_task_id", table_name="tasks")
    op.drop_constraint("fk_tasks_root_task_id_tasks", "tasks", type_="foreignkey")
    op.drop_constraint("fk_tasks_parent_task_id_tasks", "tasks", type_="foreignkey")
    op.drop_column("tasks", "task_kind")
    op.drop_column("tasks", "root_task_id")
    op.drop_column("tasks", "parent_task_id")
    sa.Enum(name="taskkind").drop(op.get_bind(), checkfirst=True)
