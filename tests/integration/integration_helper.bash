#!/usr/bin/env bash
# integration_helper.bash — Shared helpers for integration tests
# Provides functions to set up mock environments for testing the aider-task
# wrapper script, aider-cleanup, and concurrency control.

HELPER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$HELPER_DIR/../.." && pwd)"

# Paths to scripts under test
AIDER_TASK_SCRIPT="${PROJECT_ROOT}/roles/aider/files/aider-task"
AIDER_CLEANUP_SCRIPT="${PROJECT_ROOT}/roles/aider/files/aider-cleanup"
AIDER_LIB_SCRIPT="${PROJECT_ROOT}/roles/aider/files/aider-task-lib.sh"

# ─── create_test_environment ─────────────────────────────────────────────────
# Sets up a complete mock environment for integration testing.
# Creates temp directories, mock repos, mock aider binary, and environment file.
#
# Usage: create_test_environment
# Sets global variables: TEST_ENV_DIR, MOCK_REPOS_DIR, MOCK_WORKSPACE_DIR,
#   MOCK_LOG_DIR, MOCK_LOCK_DIR, MOCK_ENV_FILE, MOCK_AIDER_BIN
create_test_environment() {
    TEST_ENV_DIR="${BATS_TEST_TMPDIR}/env-$$-${RANDOM}"
    mkdir -p "$TEST_ENV_DIR"

    MOCK_REPOS_DIR="${TEST_ENV_DIR}/repos"
    MOCK_WORKSPACE_DIR="${TEST_ENV_DIR}/workspaces"
    MOCK_LOG_DIR="${TEST_ENV_DIR}/logs"
    MOCK_LOCK_DIR="${TEST_ENV_DIR}/locks"
    MOCK_AIDER_BIN="${TEST_ENV_DIR}/bin/aider"
    MOCK_ENV_FILE="${TEST_ENV_DIR}/environment"

    mkdir -p "$MOCK_REPOS_DIR"
    mkdir -p "$MOCK_WORKSPACE_DIR"
    mkdir -p "$MOCK_LOG_DIR"
    mkdir -p "$MOCK_LOCK_DIR"
    mkdir -p "${TEST_ENV_DIR}/bin"

    # Create environment file
    cat > "$MOCK_ENV_FILE" << EOF
AIDER_TASK_TIMEOUT=30
AIDER_TEST_TIMEOUT=10
AIDER_MAX_CONCURRENT=2
AIDER_QUEUE_TIMEOUT=15
AIDER_MAX_WORKSPACE_DISK=104857600
AIDER_MAX_WORKSPACE_SIZE=52428800
AIDER_EDIT_FORMAT=whole
AIDER_MAP_TOKENS=1024
AIDER_MAX_CHAT_HISTORY_TOKENS=2048
AIDER_MODEL_NAME=coder
AIDER_DEFAULT_MODE=autonomous
AIDER_API_BASE=http://127.0.0.1:8080/v1
AIDER_API_KEY=local-code
AIDER_WORKSPACE_ROOT=${MOCK_WORKSPACE_DIR}
AIDER_REPOS_DIR=${MOCK_REPOS_DIR}
AIDER_LOG_DIR=${MOCK_LOG_DIR}
AIDER_LOCK_DIR=${MOCK_LOCK_DIR}
AIDER_PROTECTED_BRANCHES=main,master,develop
EOF

    # Create a default mock Aider that does nothing (success)
    create_mock_aider "success"
}

# ─── create_mock_repo ────────────────────────────────────────────────────────
# Creates a mock git repository for testing.
#
# Usage: create_mock_repo "repo-name"
# Returns: path to the created repository
create_mock_repo() {
    local repo_name="${1:-test-repo}"
    local repo_path="${MOCK_REPOS_DIR}/${repo_name}"

    mkdir -p "$repo_path"
    git -C "$repo_path" init --initial-branch=main >/dev/null 2>&1
    git -C "$repo_path" config user.email "test@example.com"
    git -C "$repo_path" config user.name "Test User"
    git -C "$repo_path" config receive.denyCurrentBranch ignore

    # Create initial commit
    echo "# Test Repo" > "${repo_path}/README.md"
    git -C "$repo_path" add -A >/dev/null 2>&1
    git -C "$repo_path" commit -m "Initial commit" >/dev/null 2>&1

    printf '%s' "$repo_path"
}

# ─── create_mock_aider ───────────────────────────────────────────────────────
# Creates a mock Aider binary with configurable behavior.
#
# Usage: create_mock_aider "success" | "failure" | "sleep <seconds>" | "modify"
create_mock_aider() {
    local behavior="${1:-success}"
    local sleep_time="${2:-0}"

    case "$behavior" in
        success)
            cat > "$MOCK_AIDER_BIN" << 'SCRIPT'
#!/usr/bin/env bash
# Mock Aider — success (no changes)
exit 0
SCRIPT
            ;;
        failure)
            cat > "$MOCK_AIDER_BIN" << 'SCRIPT'
#!/usr/bin/env bash
# Mock Aider — failure
echo "ERROR: Mock Aider failure" >&2
exit 1
SCRIPT
            ;;
        sleep)
            cat > "$MOCK_AIDER_BIN" << SCRIPT
#!/usr/bin/env bash
# Mock Aider — sleep (for timeout/concurrency tests)
sleep ${sleep_time}
exit 0
SCRIPT
            ;;
        modify)
            cat > "$MOCK_AIDER_BIN" << 'SCRIPT'
#!/usr/bin/env bash
# Mock Aider — modifies files
echo "Modified by Aider" > new_file.txt
exit 0
SCRIPT
            ;;
    esac

    chmod +x "$MOCK_AIDER_BIN"
}

# ─── generate_test_uuid ──────────────────────────────────────────────────────
# Generates a valid UUID v4 for testing.
generate_test_uuid() {
    local hex_chars="0123456789abcdef"
    local variant_chars="89ab"
    local uuid=""
    local i

    for ((i = 0; i < 8; i++)); do uuid+="${hex_chars:$(( RANDOM % 16 )):1}"; done
    uuid+="-"
    for ((i = 0; i < 4; i++)); do uuid+="${hex_chars:$(( RANDOM % 16 )):1}"; done
    uuid+="-4"
    for ((i = 0; i < 3; i++)); do uuid+="${hex_chars:$(( RANDOM % 16 )):1}"; done
    uuid+="-"
    uuid+="${variant_chars:$(( RANDOM % 4 )):1}"
    for ((i = 0; i < 3; i++)); do uuid+="${hex_chars:$(( RANDOM % 16 )):1}"; done
    uuid+="-"
    for ((i = 0; i < 12; i++)); do uuid+="${hex_chars:$(( RANDOM % 16 )):1}"; done

    printf '%s' "$uuid"
}

# ─── create_workspace_with_retention ─────────────────────────────────────────
# Creates a mock workspace directory with a .retention_until file.
#
# Usage: create_workspace_with_retention "task-id" "2024-01-01T00:00:00Z" [repo_path]
create_workspace_with_retention() {
    local task_id="$1"
    local retention_until="$2"
    local repo_path="${3:-}"
    local ws_path="${MOCK_WORKSPACE_DIR}/${task_id}"

    mkdir -p "$ws_path"

    # Write retention file
    printf '%s\n' "$retention_until" > "${ws_path}/.retention_until"

    # If repo_path provided, create a fake .git file to simulate worktree
    if [[ -n "$repo_path" ]]; then
        printf 'gitdir: %s/.git/worktrees/%s\n' "$repo_path" "$task_id" > "${ws_path}/.git"
        # Create the worktree reference in the repo
        mkdir -p "${repo_path}/.git/worktrees/${task_id}"
        printf '%s\n' "$ws_path" > "${repo_path}/.git/worktrees/${task_id}/gitdir"
    fi

    printf '%s' "$ws_path"
}

# ─── create_active_workspace ─────────────────────────────────────────────────
# Creates a workspace that looks active (has .git but no retention file).
#
# Usage: create_active_workspace "task-id"
create_active_workspace() {
    local task_id="$1"
    local ws_path="${MOCK_WORKSPACE_DIR}/${task_id}"

    mkdir -p "$ws_path"
    # Simulate an active worktree with a .git file
    printf 'gitdir: /fake/repo/.git/worktrees/%s\n' "$task_id" > "${ws_path}/.git"

    printf '%s' "$ws_path"
}

# ─── source_cleanup_functions ─────────────────────────────────────────────────
# Sources the aider-cleanup script functions without executing main.
# This creates a modified version that can be sourced safely.
#
# Usage: eval "$(source_cleanup_functions)"
source_cleanup_functions() {
    # Remove the 'main "$@"' call and 'set -euo pipefail' for safe sourcing
    sed -e 's/^main "$@"//' \
        -e 's/^set -euo pipefail//' \
        -e '/^source \/etc\/aider\/environment/d' \
        "$AIDER_CLEANUP_SCRIPT"
}

# ─── cleanup_test_environment ────────────────────────────────────────────────
# Removes the test environment directory.
cleanup_test_environment() {
    if [[ -n "$TEST_ENV_DIR" && -d "$TEST_ENV_DIR" ]]; then
        rm -rf "$TEST_ENV_DIR"
    fi
}
