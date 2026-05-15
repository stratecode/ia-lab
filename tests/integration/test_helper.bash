#!/usr/bin/env bash
# test_helper.bash — Integration test helper for aider-task lifecycle
# Provides fixture creation, mock Aider scripts, and assertion utilities.
#
# These integration tests validate the full lifecycle of the aider-task wrapper
# by exercising the library functions and git operations directly. The production
# script (aider-task) targets Linux (flock, GNU sed) so we test the lifecycle
# logic portably by sourcing the library and orchestrating the same steps.

# ─── Paths ───────────────────────────────────────────────────────────────────
if [[ -n "${BASH_SOURCE[0]:-}" ]]; then
    HELPER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    HELPER_DIR="$(pwd)/tests/integration"
fi
PROJECT_ROOT="$(cd "$HELPER_DIR/../.." && pwd)"
AIDER_TASK_LIB="${PROJECT_ROOT}/roles/aider/files/aider-task-lib.sh"

# Source the library under test
source "$AIDER_TASK_LIB"

# ─── create_test_environment ─────────────────────────────────────────────────
# Sets up a complete test environment with:
#   - A temporary directory for all test artifacts
#   - A source git repository with an initial commit
#   - Required directory structure (workspaces, logs, locks, repos)
#   - A mock Aider binary
#
# Usage: create_test_environment
# Sets: TEST_ROOT, TEST_REPO_PATH, TEST_WORKSPACE_ROOT, TEST_LOG_DIR,
#        TEST_LOCK_DIR, TEST_REPOS_DIR, TEST_AIDER_BIN
create_test_environment() {
    TEST_ROOT="${BATS_TEST_TMPDIR}/integration-$$-${RANDOM}"
    mkdir -p "$TEST_ROOT"

    TEST_REPOS_DIR="${TEST_ROOT}/repos"
    TEST_WORKSPACE_ROOT="${TEST_ROOT}/workspaces"
    TEST_LOG_DIR="${TEST_ROOT}/logs"
    TEST_LOCK_DIR="${TEST_ROOT}/locks"
    TEST_AIDER_BIN="${TEST_ROOT}/mock-aider"

    mkdir -p "$TEST_REPOS_DIR" "$TEST_WORKSPACE_ROOT" "$TEST_LOG_DIR" "$TEST_LOCK_DIR"

    # Override library environment variables for testing
    export AIDER_REPOS_DIR="$TEST_REPOS_DIR"
    export AIDER_WORKSPACE_ROOT="$TEST_WORKSPACE_ROOT"
    export AIDER_LOG_DIR="$TEST_LOG_DIR"
    export AIDER_LOCK_DIR="$TEST_LOCK_DIR"
    export AIDER_PROTECTED_BRANCHES="main,master,develop"
    export AIDER_TASK_TIMEOUT=5
    export AIDER_TEST_TIMEOUT=3
    export AIDER_MAX_WORKSPACE_SIZE=1073741824
    export AIDER_DEFAULT_MODE=autonomous

    # Create source git repository
    TEST_REPO_PATH="${TEST_REPOS_DIR}/test-project"
    mkdir -p "${TEST_REPO_PATH}/src"
    git -C "$TEST_REPO_PATH" init --initial-branch=main >/dev/null 2>&1
    git -C "$TEST_REPO_PATH" config user.name "Test User"
    git -C "$TEST_REPO_PATH" config user.email "test@example.com"

    cat > "${TEST_REPO_PATH}/src/main.py" << 'PYEOF'
def hello():
    return "Hello, World!"

if __name__ == "__main__":
    print(hello())
PYEOF
    git -C "$TEST_REPO_PATH" add -A >/dev/null 2>&1
    git -C "$TEST_REPO_PATH" commit -m "Initial commit" >/dev/null 2>&1

    # Allow worktree operations
    git -C "$TEST_REPO_PATH" config receive.denyCurrentBranch ignore

    # Create default mock Aider (success, modifies a file)
    create_mock_aider "success"
}

# ─── create_mock_aider ───────────────────────────────────────────────────────
# Creates a mock Aider script with configurable behavior.
#
# Modes:
#   success       - Creates/modifies a file and exits 0
#   failure       - Exits with non-zero code
#   no_changes    - Exits 0 without modifying any files
#   timeout       - Sleeps indefinitely (for timeout testing)
#   forbidden     - Modifies a forbidden file (e.g., secrets/key.pem)
create_mock_aider() {
    local mode="${1:-success}"

    case "$mode" in
        success)
            cat > "$TEST_AIDER_BIN" << 'MOCKEOF'
#!/usr/bin/env bash
# Mock Aider — success mode: modifies src/main.py
cat > src/main.py << 'PYEOF'
def hello():
    return "Hello, AI World!"

def goodbye():
    return "Goodbye!"

if __name__ == "__main__":
    print(hello())
    print(goodbye())
PYEOF
exit 0
MOCKEOF
            ;;
        failure)
            cat > "$TEST_AIDER_BIN" << 'MOCKEOF'
#!/usr/bin/env bash
# Mock Aider — failure mode: exits non-zero
echo "Error: inference endpoint unreachable" >&2
exit 1
MOCKEOF
            ;;
        no_changes)
            cat > "$TEST_AIDER_BIN" << 'MOCKEOF'
#!/usr/bin/env bash
# Mock Aider — no_changes mode: exits 0 without modifying files
exit 0
MOCKEOF
            ;;
        timeout)
            cat > "$TEST_AIDER_BIN" << 'MOCKEOF'
#!/usr/bin/env bash
# Mock Aider — timeout mode: sleeps beyond timeout
sleep 3600
MOCKEOF
            ;;
        forbidden)
            cat > "$TEST_AIDER_BIN" << 'MOCKEOF'
#!/usr/bin/env bash
# Mock Aider — forbidden mode: modifies a forbidden file
cat > src/main.py << 'PYEOF'
def hello():
    return "Hello, Modified!"
PYEOF
mkdir -p secrets
echo "PRIVATE_KEY=supersecret" > secrets/credentials.pem
exit 0
MOCKEOF
            ;;
    esac

    chmod +x "$TEST_AIDER_BIN"
}

# ─── generate_task_id ────────────────────────────────────────────────────────
generate_task_id() {
    if command -v uuidgen &>/dev/null; then
        uuidgen | tr '[:upper:]' '[:lower:]'
    elif [[ -f /proc/sys/kernel/random/uuid ]]; then
        cat /proc/sys/kernel/random/uuid
    else
        printf '%04x%04x-%04x-4%03x-%x%03x-%04x%04x%04x' \
            $((RANDOM)) $((RANDOM)) $((RANDOM)) \
            $((RANDOM % 4096)) \
            $((8 + RANDOM % 4)) $((RANDOM % 4096)) \
            $((RANDOM)) $((RANDOM)) $((RANDOM))
    fi
}

# ─── create_workspace ────────────────────────────────────────────────────────
# Creates a git worktree workspace for a task (mirrors _create_workspace logic).
# Returns 0 on success, 1 on failure.
# Sets: WORKSPACE_PATH, BRANCH_NAME
create_workspace() {
    local task_id="$1"
    local description="$2"

    local slug
    slug=$(generate_branch_slug "$description")
    BRANCH_NAME="agent/${task_id}/${slug}"

    # Validate branch name
    local branch_error
    branch_error=$(validate_branch_name "$BRANCH_NAME")
    if [[ $? -ne 0 ]]; then
        echo "$branch_error" >&2
        return 1
    fi

    WORKSPACE_PATH="${TEST_WORKSPACE_ROOT}/${task_id}"

    if [[ -d "$WORKSPACE_PATH" ]]; then
        echo "workspace already exists" >&2
        return 1
    fi

    # Create git worktree
    git -C "$TEST_REPO_PATH" worktree add \
        -b "$BRANCH_NAME" \
        "$WORKSPACE_PATH" \
        "main" >/dev/null 2>&1
    local rc=$?

    if [[ $rc -ne 0 ]]; then
        echo "git worktree creation failed" >&2
        return 1
    fi

    # Configure git identity in workspace
    git -C "$WORKSPACE_PATH" config user.name "Aider Agent"
    git -C "$WORKSPACE_PATH" config user.email "aider@localhost"

    return 0
}

# ─── execute_mock_aider ──────────────────────────────────────────────────────
# Runs the mock Aider in the workspace with timeout enforcement.
# Returns: 0=success, 1=failure, 2=timeout
execute_mock_aider() {
    local workspace="$1"
    local timeout="${AIDER_TASK_TIMEOUT:-5}"

    # Use gtimeout/timeout if available for reliable process termination
    local timeout_cmd=""
    if command -v gtimeout &>/dev/null; then
        timeout_cmd="gtimeout"
    elif command -v timeout &>/dev/null; then
        timeout_cmd="timeout"
    fi

    local rc
    if [[ -n "$timeout_cmd" ]]; then
        # Use timeout command with SIGTERM then SIGKILL after 2s grace
        ( set +e; cd "$workspace" && "$timeout_cmd" --signal=TERM --kill-after=2 "${timeout}s" "$TEST_AIDER_BIN" )
        rc=$?
        # timeout command returns 124 on timeout, 137 on SIGKILL
        if [[ $rc -eq 124 || $rc -eq 137 ]]; then
            return 2  # timeout
        elif [[ $rc -ne 0 ]]; then
            return 1  # failure
        fi
        return 0
    else
        # Fallback: manual timeout with background process
        ( set +e; cd "$workspace" && "$TEST_AIDER_BIN" ) &
        local pid=$!

        local elapsed=0
        while kill -0 "$pid" 2>/dev/null; do
            if (( elapsed >= timeout )); then
                kill -TERM "$pid" 2>/dev/null || true
                sleep 2
                kill -0 "$pid" 2>/dev/null && kill -KILL "$pid" 2>/dev/null || true
                wait "$pid" 2>/dev/null || true
                return 2  # timeout
            fi
            sleep 1
            (( elapsed++ ))
        done

        wait "$pid" 2>/dev/null
        rc=$?
        if [[ $rc -ne 0 ]]; then
            return 1
        fi
        return 0
    fi
}

# ─── run_test_command ────────────────────────────────────────────────────────
# Runs a test command in the workspace with timeout.
# Returns: 0=pass, 1=fail
run_test_command() {
    local workspace="$1"
    local test_cmd="$2"
    local timeout="${AIDER_TEST_TIMEOUT:-3}"

    (cd "$workspace" && eval "$test_cmd") &
    local pid=$!

    local elapsed=0
    while kill -0 "$pid" 2>/dev/null; do
        if (( elapsed >= timeout )); then
            kill -TERM "$pid" 2>/dev/null || true
            sleep 1
            kill -0 "$pid" 2>/dev/null && kill -KILL "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
            return 1
        fi
        sleep 1
        (( elapsed++ ))
    done

    wait "$pid" 2>/dev/null
    return $?
}

# ─── create_commit ───────────────────────────────────────────────────────────
# Stages and commits changes in the workspace.
# Returns: 0=committed, 1=no_changes, 2=error
create_commit() {
    local workspace="$1"
    local task_id="$2"
    local description="$3"

    git -C "$workspace" add -A 2>/dev/null

    if git -C "$workspace" diff --cached --quiet 2>/dev/null; then
        return 1  # no changes
    fi

    local commit_msg
    commit_msg=$(format_commit_message "$task_id" "$description")

    git -C "$workspace" commit --message "$commit_msg" --no-verify >/dev/null 2>&1
    return $?
}

# ─── cleanup_test_environment ────────────────────────────────────────────────
cleanup_test_environment() {
    if [[ -n "${TEST_ROOT:-}" && -d "${TEST_ROOT:-}" ]]; then
        if [[ -d "$TEST_REPO_PATH" ]]; then
            git -C "$TEST_REPO_PATH" worktree prune 2>/dev/null || true
        fi
        rm -rf "$TEST_ROOT"
    fi
}

# ─── Assertion Helpers ───────────────────────────────────────────────────────

assert_file_exists() {
    local path="$1"
    if [[ ! -f "$path" ]]; then
        echo "ASSERTION FAILED: file does not exist: $path" >&2
        return 1
    fi
}

assert_dir_exists() {
    local path="$1"
    if [[ ! -d "$path" ]]; then
        echo "ASSERTION FAILED: directory does not exist: $path" >&2
        return 1
    fi
}

assert_dir_not_exists() {
    local path="$1"
    if [[ -d "$path" ]]; then
        echo "ASSERTION FAILED: directory should not exist: $path" >&2
        return 1
    fi
}
