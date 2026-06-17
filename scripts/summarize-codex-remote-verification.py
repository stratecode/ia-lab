#!/usr/bin/env python3
import json
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
SOAK_ROOT = ROOT / "tmp" / "verify-codex-lab-remote-soak"
STRONG_ROOT = ROOT / "tmp" / "verify-codex-lab-remote"


def load_json(path: Path):
    with path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def count_lines(path: Path, prefix: str) -> int:
    if not path.exists():
        return 0
    count = 0
    with path.open("r", encoding="utf-8", errors="replace") as handle:
        for line in handle:
            if line.startswith(prefix):
                count += 1
    return count


def main():
    soak_reports = sorted(SOAK_ROOT.glob("*/report.json"))
    reports = [load_json(path) for path in soak_reports]

    aggregate_runs = sum(item["runs_requested"] for item in reports)
    aggregate_pass = sum(item["pass_count"] for item in reports)
    aggregate_fail = sum(item["fail_count"] for item in reports)

    strong_real_repo_log = STRONG_ROOT / "real-repo.log"
    strong_real_repo_worktree_log = STRONG_ROOT / "real-repo-worktree.log"

    summary = {
        "report_count": len(reports),
        "soak_reports": [str(path.relative_to(ROOT)) for path in soak_reports],
        "aggregate_runs_requested": aggregate_runs,
        "aggregate_pass_count": aggregate_pass,
        "aggregate_fail_count": aggregate_fail,
        "aggregate_success_rate": (aggregate_pass / aggregate_runs) if aggregate_runs else 0.0,
        "real_repo_verified": strong_real_repo_log.exists(),
        "tracked_file_worktree_verified": strong_real_repo_worktree_log.exists(),
        "strong_verifier_temp_trials_logged": len(list(STRONG_ROOT.glob("temp-trial-*.log"))),
        "strong_verifier_real_repo_mentions": count_lines(strong_real_repo_log, "provider:")
        if strong_real_repo_log.exists()
        else 0,
    }

    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
