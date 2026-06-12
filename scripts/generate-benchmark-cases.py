#!/usr/bin/env python3
import argparse
import json
from pathlib import Path

PROBLEM_DOMAIN_ALIASES = {
    "http_client": "http_client",
    "http": "http_client",
    "data_validation": "data_validation",
    "cli": "cli",
    "web": "web",
    "logging": "logging",
    "auth": "auth",
    "tui": "tui",
    "json": "json",
    "serialization": "serialization",
    "test": "test",
    "math": "math",
}

FRAMEWORK_ALIASES = {
    "library": "library",
    "framework": "framework",
    "sdk": "sdk",
    "parser": "parser",
    "processor": "processor",
}


def runtime_defaults(runtime: str) -> dict:
    runtime = (runtime or "").strip().lower()
    if runtime == "python":
        return {
            "test_command": [
                "docker",
                "run",
                "--rm",
                "-v",
                "{{ project_root_abs }}:/src",
                "-w",
                "/src",
                "python:3.12",
                "/bin/bash",
                "-lc",
                "python - <<'PY'\nfrom pathlib import Path\nimport tomllib\nmanifest = Path('pyproject.toml')\nif not manifest.exists():\n    raise SystemExit(1)\ndata = tomllib.loads(manifest.read_text())\nproject = data.get('project') or {}\nname = project.get('name') or 'unknown'\nprint(name)\nPY",
            ],
            "expected_files": ["pyproject.toml"],
        }
    if runtime in {"javascript", "typescript"}:
        return {
            "test_command": [
                "docker",
                "run",
                "--rm",
                "-v",
                "{{ project_root_abs }}:/src",
                "-w",
                "/src",
                "node:22",
                "/bin/bash",
                "-lc",
                "node -e \"const fs=require('fs'); const pkg=JSON.parse(fs.readFileSync('package.json','utf8')); if(!pkg.name){process.exit(1)} console.log(pkg.name)\"",
            ],
            "expected_files": ["package.json"],
        }
    if runtime == "php":
        return {
            "test_command": [
                "docker",
                "run",
                "--rm",
                "-v",
                "{{ project_root_abs }}:/src",
                "-w",
                "/src",
                "composer:2",
                "/bin/bash",
                "-lc",
                "composer validate --no-check-all --strict",
            ],
            "expected_files": ["composer.json"],
        }
    if runtime == "go":
        return {
            "test_command": [
                "docker",
                "run",
                "--rm",
                "-v",
                "{{ project_root_abs }}:/src",
                "-w",
                "/src",
                "alpine:3.20",
                "/bin/sh",
                "-lc",
                "test -f go.mod && grep -q '^module ' go.mod",
            ],
            "expected_files": ["go.mod"],
        }
    if runtime == "rust":
        return {
            "test_command": [
                "docker",
                "run",
                "--rm",
                "-v",
                "{{ project_root_abs }}:/src",
                "-w",
                "/src",
                "alpine:3.20",
                "/bin/sh",
                "-lc",
                "test -f Cargo.toml && grep -q '\\[package\\]' Cargo.toml",
            ],
            "expected_files": ["Cargo.toml"],
        }
    if runtime == "c":
        return {
            "test_command": [
                "docker",
                "run",
                "--rm",
                "-v",
                "{{ project_root_abs }}:/src",
                "-w",
                "/src",
                "alpine:3.20",
                "/bin/sh",
                "-lc",
                "test -f README.md && (test -f configure.ac || test -f CMakeLists.txt || test -f Makefile || test -f meson.build)",
            ],
            "expected_files": ["README.md"],
        }
    raise SystemExit(f"unsupported runtime_or_stack: {runtime}")


REPO_OVERRIDES = {}

SEQUENCE_OVERRIDES = {
    "monolog": {"benchmark_league": "technology_transfer", "sequence_id": "technology_transfer_php", "sequence_position": 1},
    "math-php": {"benchmark_league": "technology_transfer", "sequence_id": "technology_transfer_php", "sequence_position": 2},
    "slim": {"benchmark_league": "technology_transfer", "sequence_id": "technology_transfer_php", "sequence_position": 3},
    "cobra": {"benchmark_league": "technology_transfer", "sequence_id": "technology_transfer_go", "sequence_position": 1},
    "bubbletea": {"benchmark_league": "technology_transfer", "sequence_id": "technology_transfer_go", "sequence_position": 2},
    "auth-go": {"benchmark_league": "technology_transfer", "sequence_id": "technology_transfer_go", "sequence_position": 3},
    "axios": {"benchmark_league": "pattern_transfer", "sequence_id": "pattern_transfer_http_clients", "sequence_position": 1},
    "httpx": {"benchmark_league": "pattern_transfer", "sequence_id": "pattern_transfer_http_clients", "sequence_position": 2},
    "jq": {"benchmark_league": "pattern_transfer", "sequence_id": "pattern_transfer_json", "sequence_position": 1},
    "cjson": {"benchmark_league": "pattern_transfer", "sequence_id": "pattern_transfer_json", "sequence_position": 2},
    "pydantic": {"benchmark_league": "repo_recall", "sequence_id": "repo_recall_python", "sequence_position": 1},
    "typer": {"benchmark_league": "repo_recall", "sequence_id": "repo_recall_python", "sequence_position": 1},
    "vitest": {"benchmark_league": "repo_recall", "sequence_id": "repo_recall_typescript", "sequence_position": 1},
    "fastify": {"benchmark_league": "repo_recall", "sequence_id": "repo_recall_javascript", "sequence_position": 1},
    "serde": {"benchmark_league": "repo_recall", "sequence_id": "repo_recall_rust", "sequence_position": 1},
}

def analyze_profile(repo_profile: str, runtime: str) -> dict:
    tokens = [token for token in (repo_profile or "").strip().lower().split("_") if token]
    framework = "library"
    problem_domain = tokens[1] if len(tokens) > 1 else runtime
    for token in reversed(tokens):
        if token in FRAMEWORK_ALIASES:
            framework = FRAMEWORK_ALIASES[token]
            break
    for token in tokens:
        if token in PROBLEM_DOMAIN_ALIASES:
            problem_domain = PROBLEM_DOMAIN_ALIASES[token]
            break
    language = runtime
    return {
        "language": language,
        "framework": framework,
        "problem_domain": problem_domain,
        "error_class": "repository_structure_validation",
        "fix_pattern": "deterministic_marker_before_review",
        "validation_pattern": "containerized_manifest_smoke",
    }


def sequence_metadata(repo_id: str, repo_profile: str, runtime: str, memory_shape: dict) -> dict:
    override = SEQUENCE_OVERRIDES.get(repo_id)
    if override:
        league = override["benchmark_league"]
        sequence_id = override["sequence_id"]
        sequence_position = override["sequence_position"]
    else:
        league = "repo_recall"
        sequence_id = f"repo_recall_{runtime}_{repo_id}"
        sequence_position = 1
    if league == "pattern_transfer" and memory_shape["problem_domain"] not in {"http_client", "cli", "json", "data_validation"}:
        league = "technology_transfer"
        sequence_id = f"technology_transfer_{runtime}_{repo_id}"
        sequence_position = 1
    return {
        "benchmark_league": league,
        "sequence_id": sequence_id,
        "sequence_position": sequence_position,
    }


def build_case(repo: dict) -> dict:
    repo_id = repo["id"]
    runtime = repo["runtime_or_stack"]
    defaults = runtime_defaults(runtime)
    overrides = REPO_OVERRIDES.get(repo_id, {})
    memory_shape = analyze_profile(repo["repo_profile"], runtime)
    sequence_shape = sequence_metadata(repo_id, repo["repo_profile"], runtime, memory_shape)
    expected_files = overrides.get("expected_files", defaults["expected_files"])
    test_command = overrides.get("test_command", defaults["test_command"])
    case = {
        "id": f"{repo_id}-experience-review",
        "repo_id": repo_id,
        "case_type": "review_only",
        "goal": f"Run a deterministic experience benchmark workflow for {repo_id} and validate the repository smoke review command.",
        "repo_profile": repo["repo_profile"],
        "project_type": "existing_repo",
        "runtime_or_stack": runtime,
        "project_root": ".",
        "test_focus": "experience benchmark smoke review",
        "test_command": test_command,
        "expected_files": expected_files,
        "coder_tool": "write_file",
        "patch_target": ".lab/repo-workflow-marker.txt",
        "write_content": f"{repo_id} benchmark marker\n",
        "coder_summary": f"Write a deterministic benchmark marker file for {repo_id} before repository review.",
        "memory_expectation": "helpful",
    }
    case.update(memory_shape)
    case.update(sequence_shape)
    return case


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repos", required=True)
    parser.add_argument("--outdir", required=True)
    parser.add_argument("--repo-ids", default="")
    args = parser.parse_args()

    repos = json.loads(Path(args.repos).read_text())
    repo_filter = {item.strip() for item in args.repo_ids.split(",") if item.strip()}

    outdir = Path(args.outdir)
    outdir.mkdir(parents=True, exist_ok=True)

    if repo_filter:
        repo_map = {repo["id"]: repo for repo in repos}
        ordered_repos = [repo_map[repo_id] for repo_id in args.repo_ids.split(",") if repo_id.strip() in repo_map]
    else:
        ordered_repos = repos

    for repo in ordered_repos:
        case = build_case(repo)
        (outdir / f"{case['id']}.json").write_text(json.dumps(case, indent=2) + "\n")


if __name__ == "__main__":
    main()
