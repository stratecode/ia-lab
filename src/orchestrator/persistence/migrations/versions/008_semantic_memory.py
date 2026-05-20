"""Add semantic memory chunks backed by pgvector.

Revision ID: 008_semantic_memory
Revises: 007_initiatives
Create Date: 2026-05-21
"""

from alembic import op


revision = "008_semantic_memory"
down_revision = "007_initiatives"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("CREATE EXTENSION IF NOT EXISTS vector")
    op.execute(
        """
        CREATE TABLE IF NOT EXISTS semantic_chunks (
          id UUID PRIMARY KEY,
          source_type TEXT NOT NULL,
          source_id UUID NOT NULL,
          initiative_id UUID NULL,
          task_id UUID NULL,
          artifact_id UUID NULL,
          workspace_root TEXT NULL,
          chunk_index INTEGER NOT NULL,
          content_text TEXT NOT NULL,
          content_hash TEXT NOT NULL,
          metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
          embedding vector(1024) NOT NULL,
          created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
          updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
          UNIQUE (source_type, source_id, chunk_index, content_hash)
        )
        """
    )
    op.execute("CREATE INDEX IF NOT EXISTS ix_semantic_chunks_source ON semantic_chunks (source_type, source_id)")
    op.execute("CREATE INDEX IF NOT EXISTS ix_semantic_chunks_initiative_id ON semantic_chunks (initiative_id)")
    op.execute("CREATE INDEX IF NOT EXISTS ix_semantic_chunks_task_id ON semantic_chunks (task_id)")
    op.execute("CREATE INDEX IF NOT EXISTS ix_semantic_chunks_artifact_id ON semantic_chunks (artifact_id)")
    op.execute("CREATE INDEX IF NOT EXISTS ix_semantic_chunks_workspace_root ON semantic_chunks (workspace_root)")
    op.execute("CREATE INDEX IF NOT EXISTS ix_semantic_chunks_metadata ON semantic_chunks USING GIN (metadata)")
    op.execute(
        """
        CREATE INDEX IF NOT EXISTS ix_semantic_chunks_embedding_cosine
        ON semantic_chunks USING ivfflat (embedding vector_cosine_ops)
        WITH (lists = 100)
        """
    )


def downgrade() -> None:
    op.execute("DROP TABLE IF EXISTS semantic_chunks")
