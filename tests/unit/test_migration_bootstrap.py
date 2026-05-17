from __future__ import annotations

from orchestrator.persistence.migrations import _detect_legacy_revision


class _FakeInspector:
    def __init__(self, tables: list[str], task_columns: list[str] | None = None) -> None:
        self._tables = tables
        self._task_columns = task_columns or []

    def get_table_names(self) -> list[str]:
        return list(self._tables)

    def get_columns(self, table_name: str) -> list[dict[str, str]]:
        if table_name != "tasks":
            return []
        return [{"name": name} for name in self._task_columns]


def test_detect_legacy_revision_returns_none_for_fresh_database(monkeypatch) -> None:
    monkeypatch.setattr(
        "orchestrator.persistence.migrations.inspect",
        lambda connection: _FakeInspector([]),
    )

    assert _detect_legacy_revision(object()) is None


def test_detect_legacy_revision_bootstraps_initial_schema(monkeypatch) -> None:
    monkeypatch.setattr(
        "orchestrator.persistence.migrations.inspect",
        lambda connection: _FakeInspector(
            ["tasks", "approvals"],
            task_columns=["id", "description", "state"],
        ),
    )

    assert _detect_legacy_revision(object()) == "001"


def test_detect_legacy_revision_bootstraps_head_for_hierarchy_schema(monkeypatch) -> None:
    monkeypatch.setattr(
        "orchestrator.persistence.migrations.inspect",
        lambda connection: _FakeInspector(
            ["tasks", "approvals"],
            task_columns=[
                "id",
                "description",
                "state",
                "parent_task_id",
                "root_task_id",
                "task_kind",
            ],
        ),
    )

    assert _detect_legacy_revision(object()) == "002"


def test_detect_legacy_revision_skips_stamped_database(monkeypatch) -> None:
    monkeypatch.setattr(
        "orchestrator.persistence.migrations.inspect",
        lambda connection: _FakeInspector(["alembic_version", "tasks"]),
    )

    assert _detect_legacy_revision(object()) is None
