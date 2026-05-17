"""Add execution_target to tasks and create local_bridges table.

Revision ID: 003
Revises: 002
Create Date: 2026-05-17 00:30:00.000000
"""

from typing import Sequence, Union

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import JSONB, UUID


revision: str = "003"
down_revision: Union[str, None] = "002"
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    executiontarget_enum = sa.Enum("remote", "local", name="executiontarget")
    executiontarget_enum.create(op.get_bind(), checkfirst=True)

    op.add_column(
        "tasks",
        sa.Column(
            "execution_target",
            executiontarget_enum,
            nullable=False,
            server_default="remote",
        ),
    )
    op.create_index(
        "ix_tasks_execution_target_state",
        "tasks",
        ["execution_target", "state"],
    )

    op.create_table(
        "local_bridges",
        sa.Column("id", UUID(as_uuid=True), primary_key=True, nullable=False),
        sa.Column("name", sa.String(length=128), nullable=False),
        sa.Column("hostname", sa.String(length=255), nullable=False),
        sa.Column("workspace_root", sa.String(length=1024), nullable=False),
        sa.Column("status", sa.String(length=32), nullable=False, server_default="active"),
        sa.Column("capabilities", JSONB, nullable=True, server_default=sa.text("'{}'::jsonb")),
        sa.Column("api_key_name", sa.String(length=255), nullable=True),
        sa.Column("last_heartbeat", sa.DateTime(timezone=True), nullable=True),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
        sa.Column("updated_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
    )
    op.create_index("ix_local_bridges_workspace_root", "local_bridges", ["workspace_root"])
    op.create_index("ix_local_bridges_last_heartbeat", "local_bridges", ["last_heartbeat"])


def downgrade() -> None:
    op.drop_index("ix_local_bridges_last_heartbeat", table_name="local_bridges")
    op.drop_index("ix_local_bridges_workspace_root", table_name="local_bridges")
    op.drop_table("local_bridges")
    op.drop_index("ix_tasks_execution_target_state", table_name="tasks")
    op.drop_column("tasks", "execution_target")
    sa.Enum(name="executiontarget").drop(op.get_bind(), checkfirst=True)
