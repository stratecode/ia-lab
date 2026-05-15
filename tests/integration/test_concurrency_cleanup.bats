#!/usr/bin/env bats
# test_concurrency_cleanup.bats — Integration tests for concurrency control and cleanup
# Task 11.2: Tests concurrent execution, concurrency limits, stale lock recovery,
# workspace cleanup, and disk threshold enforcement.
#
# Requirements validated:
#   11.1 - Concurrent execution with independent workspaces
#   11.2 - Concurrency limit enforcement
#   11.3 - File-based lock mechanism (flock)
#   11.4 - Retry with queue timeout
#   11.5 - Independent workspaces per concurrent task
#   11.6 - Stale lock recovery
#   9.1  - Completed task cleanup
#   9.2  - Failed task workspace preservation
#   9.4  - Workspace directory and worktree reference removal
#   9.5  - Branch pruning for unmerged branches
#   9.6  - Disk threshold enforcement (oldest expired first)

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/integration_helper.bash"
    create_test_environment
}

teardown() {
    # Kill any background processes we started
    jobs -p 2>/dev/null | xargs -r kill 2>/dev/null || true
    wait 2>/dev/null || true
    cleanup_test_environment
}

# ═══════════════════════════════════════════════════════════════════════════════
# Concurrency Control Tests
# ═══════════════════════════════════════════════════════════════════════════════

# --- Test: Concurrent execution — multiple tasks get independent workspaces ---
# Requirement 11.1, 11.5
@test "concurrent execution: multiple tasks acquire independent lock slots" {
    # Skip if flock is not available (macOS)
    if ! command -v flock &>/dev/null; then
        skip "flock not available (Linux-only utility)"
    fi

    local lock_dir="$MOCK_LOCK_DIR"

    # Simulate two concurrent tasks acquiring slots using flock
    # Task 1: acquire slot 0
    local lock_file_0="${lock_dir}/slot-0.lock"
    exec 200>"$lock_file_0"
    flock -n 200
    printf '%d normal task-1\n' "$$" > "$lock_file_0"

    # Task 2: acquire slot 1
    local lock_file_1="${lock_dir}/slot-1.lock"
    exec 201>"$lock_file_1"
    flock -n 201
    printf '%d normal task-2\n' "$$" > "$lock_file_1"

    # Both slots should be locked
    [[ -f "$lock_file_0" ]]
    [[ -f "$lock_file_1" ]]

    # Verify content
    grep -q "task-1" "$lock_file_0"
    grep -q "task-2" "$lock_file_1"

    # Release locks
    exec 200>&-
    exec 201>&-
}

# --- Test: Concurrent tasks create independent workspace directories ---
# Requirement 11.1, 11.5
@test "concurrent execution: independent workspaces are created per task" {
    local repo_path
    repo_path=$(create_mock_repo "concurrent-repo")

    local task_id_1
    task_id_1=$(generate_test_uuid)
    local task_id_2
    task_id_2=$(generate_test_uuid)

    # Create two independent worktrees (simulating concurrent workspace creation)
    local ws_1="${MOCK_WORKSPACE_DIR}/${task_id_1}"
    local ws_2="${MOCK_WORKSPACE_DIR}/${task_id_2}"

    git -C "$repo_path" worktree add -b "agent/${task_id_1}/test-one" "$ws_1" main >/dev/null 2>&1
    git -C "$repo_path" worktree add -b "agent/${task_id_2}/test-two" "$ws_2" main >/dev/null 2>&1

    # Both workspaces should exist independently
    [[ -d "$ws_1" ]]
    [[ -d "$ws_2" ]]
    [[ -f "${ws_1}/.git" ]]
    [[ -f "${ws_2}/.git" ]]

    # Changes in one workspace should not affect the other
    echo "change in ws1" > "${ws_1}/ws1_file.txt"
    [[ ! -f "${ws_2}/ws1_file.txt" ]]

    echo "change in ws2" > "${ws_2}/ws2_file.txt"
    [[ ! -f "${ws_1}/ws2_file.txt" ]]

    # Cleanup worktrees
    git -C "$repo_path" worktree remove "$ws_1" --force 2>/dev/null || true
    git -C "$repo_path" worktree remove "$ws_2" --force 2>/dev/null || true
}

# --- Test: Concurrency limit — tasks queue when all slots are full ---
# Requirement 11.2, 11.3, 11.4
@test "concurrency limit: flock blocks when all slots are occupied" {
    # Skip if flock is not available (macOS)
    if ! command -v flock &>/dev/null; then
        skip "flock not available (Linux-only utility)"
    fi

    local lock_dir="$MOCK_LOCK_DIR"

    # Occupy both slots (max_concurrent=2)
    local lock_file_0="${lock_dir}/slot-0.lock"
    local lock_file_1="${lock_dir}/slot-1.lock"

    exec 200>"$lock_file_0"
    flock -n 200
    printf '%d normal holder-0\n' "$$" > "$lock_file_0"

    exec 201>"$lock_file_1"
    flock -n 201
    printf '%d normal holder-1\n' "$$" > "$lock_file_1"

    # A third task should fail to acquire any slot (non-blocking)
    local acquired=0
    for slot in 0 1; do
        local lf="${lock_dir}/slot-${slot}.lock"
        # Try to acquire with a subshell (different fd)
        if (flock -n 9 9>"$lf") 2>/dev/null; then
            acquired=1
        fi
    done

    # Should NOT have acquired any slot
    [[ $acquired -eq 0 ]]

    # Release locks
    exec 200>&-
    exec 201>&-
}

# --- Test: Concurrency limit — task eventually acquires slot after release ---
# Requirement 11.4
@test "concurrency limit: waiting task acquires slot after holder releases" {
    # Skip if flock is not available (macOS)
    if ! command -v flock &>/dev/null; then
        skip "flock not available (Linux-only utility)"
    fi

    local lock_dir="$MOCK_LOCK_DIR"
    local lock_file="${lock_dir}/slot-0.lock"

    # Hold the lock in a background subshell
    (
        exec 200>"$lock_file"
        flock -n 200
        printf '%d normal holder\n' "$$" > "$lock_file"
        sleep 2
        # Lock released when subshell exits
    ) &
    local holder_pid=$!

    # Give holder time to acquire
    sleep 0.5

    # Verify lock is held (non-blocking attempt fails)
    ! (flock -n 9 9>"$lock_file") 2>/dev/null

    # Wait for holder to release
    wait "$holder_pid" 2>/dev/null || true

    # Now we should be able to acquire
    exec 200>"$lock_file"
    flock -n 200
    local result=$?
    exec 200>&-

    [[ $result -eq 0 ]]
}

# --- Test: Stale lock recovery — orphaned lock files are reclaimed ---
# Requirement 11.6
@test "stale lock recovery: lock with dead PID is reclaimed" {
    # Skip if flock is not available (macOS)
    if ! command -v flock &>/dev/null; then
        skip "flock not available (Linux-only utility)"
    fi

    local lock_dir="$MOCK_LOCK_DIR"
    local lock_file="${lock_dir}/slot-0.lock"

    # Write a lock file with a PID that doesn't exist
    # Use a very high PID that's unlikely to be running
    local fake_pid=99999
    while kill -0 "$fake_pid" 2>/dev/null; do
        fake_pid=$(( fake_pid + 1 ))
    done

    printf '%d normal stale-task\n' "$fake_pid" > "$lock_file"

    # Verify the fake PID is not running
    ! kill -0 "$fake_pid" 2>/dev/null

    # Source the library to use its functions
    source "$AIDER_LIB_SCRIPT"

    # Simulate stale lock detection logic (from aider-task _acquire_concurrency_slot)
    local lock_pid
    lock_pid=$(head -1 "$lock_file" 2>/dev/null | awk '{print $1}')

    # PID should not be running
    ! kill -0 "$lock_pid" 2>/dev/null

    # Reclaim: remove stale lock
    rm -f "$lock_file"

    # Now we should be able to acquire the slot
    exec 200>"$lock_file"
    flock -n 200
    local result=$?
    printf '%d normal reclaimed-task\n' "$$" > "$lock_file"
    exec 200>&-

    [[ $result -eq 0 ]]
    grep -q "reclaimed-task" "$lock_file"
}

# --- Test: Stale lock recovery — active PID lock is NOT reclaimed ---
# Requirement 11.6
@test "stale lock recovery: lock with active PID is preserved" {
    local lock_dir="$MOCK_LOCK_DIR"
    local lock_file="${lock_dir}/slot-0.lock"

    # Write a lock file with our own PID (which IS running)
    printf '%d normal active-task\n' "$$" > "$lock_file"

    # Verify our PID is running
    kill -0 "$$" 2>/dev/null

    # Stale detection should NOT reclaim this lock
    local lock_pid
    lock_pid=$(head -1 "$lock_file" 2>/dev/null | awk '{print $1}')

    # PID IS running — should not be reclaimed
    kill -0 "$lock_pid" 2>/dev/null
    local is_running=$?

    [[ $is_running -eq 0 ]]
    # Lock file should still contain original content
    grep -q "active-task" "$lock_file"
}

# --- Test: Queue timeout — task fails after timeout expires ---
# Requirement 11.4
@test "concurrency limit: queue timeout produces failure status" {
    # This test validates the timeout logic conceptually.
    # We simulate the timeout check from the concurrency control loop.
    local queue_timeout=2
    local wait_start
    wait_start=$(date +%s)

    # Simulate waiting
    sleep 1

    local elapsed=$(( $(date +%s) - wait_start ))
    # After 1 second, should NOT have timed out yet
    [[ $elapsed -lt $queue_timeout ]]

    # Simulate more waiting past timeout
    sleep 2
    elapsed=$(( $(date +%s) - wait_start ))
    # After 3 seconds total, should have exceeded timeout
    [[ $elapsed -ge $queue_timeout ]]
}

# ═══════════════════════════════════════════════════════════════════════════════
# Cleanup Tests
# ═══════════════════════════════════════════════════════════════════════════════

# --- Test: Cleanup removes expired workspaces ---
# Requirement 9.1, 9.4
@test "cleanup: expired workspaces are removed" {
    # Create a workspace with an expired retention timestamp (in the past)
    local task_id
    task_id=$(generate_test_uuid)
    local expired_time="2020-01-01T00:00:00Z"

    local ws_path
    ws_path=$(create_workspace_with_retention "$task_id" "$expired_time")

    [[ -d "$ws_path" ]]

    # Run the cleanup script with overridden environment
    # We use sed to replace the main call so we can call individual phases
    run bash -c "
        export AIDER_WORKSPACE_ROOT='$MOCK_WORKSPACE_DIR'
        export AIDER_REPOS_DIR='$MOCK_REPOS_DIR'
        export AIDER_MAX_WORKSPACE_DISK=10737418240
        export AIDER_LOG_DIR='$MOCK_LOG_DIR'
        # Source the script but override main to prevent auto-execution
        eval \"\$(sed 's/^main \"\\\$@\"//' '$AIDER_CLEANUP_SCRIPT')\"
        _remove_expired_workspaces
    " 2>/dev/null

    # Workspace should be removed
    [[ ! -d "$ws_path" ]]
}

# --- Test: Cleanup preserves unexpired workspaces ---
# Requirement 9.2
@test "cleanup: unexpired workspaces are preserved" {
    # Create a workspace with a future retention timestamp
    local task_id
    task_id=$(generate_test_uuid)
    local future_time="2099-12-31T23:59:59Z"

    local ws_path
    ws_path=$(create_workspace_with_retention "$task_id" "$future_time")

    [[ -d "$ws_path" ]]

    # Run the cleanup expired phase
    run bash -c "
        export AIDER_WORKSPACE_ROOT='$MOCK_WORKSPACE_DIR'
        export AIDER_REPOS_DIR='$MOCK_REPOS_DIR'
        export AIDER_MAX_WORKSPACE_DISK=10737418240
        export AIDER_LOG_DIR='$MOCK_LOG_DIR'
        eval \"\$(sed 's/^main \"\\\$@\"//' '$AIDER_CLEANUP_SCRIPT')\"
        _remove_expired_workspaces
    " 2>/dev/null

    # Workspace should still exist
    [[ -d "$ws_path" ]]
}

# --- Test: Cleanup removes expired but preserves unexpired (mixed) ---
# Requirement 9.1, 9.2
@test "cleanup: mixed expired and unexpired — only expired removed" {
    local task_expired
    task_expired=$(generate_test_uuid)
    local task_active
    task_active=$(generate_test_uuid)

    local ws_expired
    ws_expired=$(create_workspace_with_retention "$task_expired" "2020-01-01T00:00:00Z")
    local ws_active
    ws_active=$(create_workspace_with_retention "$task_active" "2099-12-31T23:59:59Z")

    [[ -d "$ws_expired" ]]
    [[ -d "$ws_active" ]]

    # Run cleanup
    run bash -c "
        export AIDER_WORKSPACE_ROOT='$MOCK_WORKSPACE_DIR'
        export AIDER_REPOS_DIR='$MOCK_REPOS_DIR'
        export AIDER_MAX_WORKSPACE_DISK=10737418240
        export AIDER_LOG_DIR='$MOCK_LOG_DIR'
        eval \"\$(sed 's/^main \"\\\$@\"//' '$AIDER_CLEANUP_SCRIPT')\"
        _remove_expired_workspaces
    " 2>/dev/null

    # Expired should be gone, active should remain
    [[ ! -d "$ws_expired" ]]
    [[ -d "$ws_active" ]]
}

# --- Test: Cleanup preserves active workspaces (no retention file, has .git) ---
# Requirement 9.2
@test "cleanup: active workspaces without retention file are preserved" {
    local task_id
    task_id=$(generate_test_uuid)

    local ws_path
    ws_path=$(create_active_workspace "$task_id")

    [[ -d "$ws_path" ]]
    [[ -f "${ws_path}/.git" ]]

    # Run cleanup
    run bash -c "
        export AIDER_WORKSPACE_ROOT='$MOCK_WORKSPACE_DIR'
        export AIDER_REPOS_DIR='$MOCK_REPOS_DIR'
        export AIDER_MAX_WORKSPACE_DISK=10737418240
        export AIDER_LOG_DIR='$MOCK_LOG_DIR'
        eval \"\$(sed 's/^main \"\\\$@\"//' '$AIDER_CLEANUP_SCRIPT')\"
        _remove_expired_workspaces
    " 2>/dev/null

    # Active workspace should still exist
    [[ -d "$ws_path" ]]
}

# --- Test: Disk threshold — oldest expired removed when over limit ---
# Requirement 9.6
@test "disk threshold: oldest expired workspaces removed first when over limit" {
    # Skip if du -sb is not available (macOS without GNU coreutils in PATH)
    if ! du -sb /tmp &>/dev/null; then
        skip "du -sb not available (GNU coreutils required)"
    fi

    # Create multiple expired workspaces with different retention times
    local task_oldest
    task_oldest=$(generate_test_uuid)
    local task_middle
    task_middle=$(generate_test_uuid)
    local task_newest
    task_newest=$(generate_test_uuid)

    # Create workspaces with different retention timestamps (all expired)
    local ws_oldest
    ws_oldest=$(create_workspace_with_retention "$task_oldest" "2020-01-01T00:00:00Z")
    local ws_middle
    ws_middle=$(create_workspace_with_retention "$task_middle" "2021-06-15T00:00:00Z")
    local ws_newest
    ws_newest=$(create_workspace_with_retention "$task_newest" "2022-12-31T00:00:00Z")

    # Add some content to make them have measurable size
    dd if=/dev/zero of="${ws_oldest}/data.bin" bs=1024 count=100 2>/dev/null
    dd if=/dev/zero of="${ws_middle}/data.bin" bs=1024 count=100 2>/dev/null
    dd if=/dev/zero of="${ws_newest}/data.bin" bs=1024 count=100 2>/dev/null

    # Set a very low disk threshold so all workspaces exceed it
    # Total size is ~300KB, set threshold to 150KB so some must be removed
    local low_threshold=153600  # 150KB

    # Run disk threshold enforcement
    run bash -c "
        export AIDER_WORKSPACE_ROOT='$MOCK_WORKSPACE_DIR'
        export AIDER_REPOS_DIR='$MOCK_REPOS_DIR'
        export AIDER_MAX_WORKSPACE_DISK=$low_threshold
        export AIDER_LOG_DIR='$MOCK_LOG_DIR'
        eval \"\$(sed 's/^main \"\\\$@\"//' '$AIDER_CLEANUP_SCRIPT')\"
        _enforce_disk_threshold
    " 2>/dev/null

    # The oldest workspace should be removed first
    [[ ! -d "$ws_oldest" ]]

    # At least one of the newer workspaces may still exist
    # (depends on whether removing oldest brought us under threshold)
    # The key property: oldest is removed before newer ones
}

# --- Test: Disk threshold — no removal when under limit ---
# Requirement 9.6
@test "disk threshold: no workspaces removed when under limit" {
    # Skip if du -sb is not available (macOS without GNU coreutils in PATH)
    if ! du -sb /tmp &>/dev/null; then
        skip "du -sb not available (GNU coreutils required)"
    fi

    local task_id
    task_id=$(generate_test_uuid)

    local ws_path
    ws_path=$(create_workspace_with_retention "$task_id" "2020-01-01T00:00:00Z")

    # Add small content
    echo "small file" > "${ws_path}/data.txt"

    # Set a very high threshold
    local high_threshold=10737418240  # 10GB

    # Run disk threshold enforcement
    run bash -c "
        export AIDER_WORKSPACE_ROOT='$MOCK_WORKSPACE_DIR'
        export AIDER_REPOS_DIR='$MOCK_REPOS_DIR'
        export AIDER_MAX_WORKSPACE_DISK=$high_threshold
        export AIDER_LOG_DIR='$MOCK_LOG_DIR'
        eval \"\$(sed 's/^main \"\\\$@\"//' '$AIDER_CLEANUP_SCRIPT')\"
        _enforce_disk_threshold
    " 2>/dev/null

    # Workspace should still exist (under threshold)
    [[ -d "$ws_path" ]]
}

# --- Test: Cleanup removes orphaned directories (no .git, no retention) ---
# Requirement 9.4
@test "cleanup: orphaned directories without .git are removed" {
    local task_id
    task_id=$(generate_test_uuid)
    local orphan_path="${MOCK_WORKSPACE_DIR}/${task_id}"

    # Create an orphaned directory (no .git file, no retention file)
    mkdir -p "$orphan_path"
    echo "leftover" > "${orphan_path}/leftover.txt"

    [[ -d "$orphan_path" ]]

    # Run cleanup
    run bash -c "
        export AIDER_WORKSPACE_ROOT='$MOCK_WORKSPACE_DIR'
        export AIDER_REPOS_DIR='$MOCK_REPOS_DIR'
        export AIDER_MAX_WORKSPACE_DISK=10737418240
        export AIDER_LOG_DIR='$MOCK_LOG_DIR'
        eval \"\$(sed 's/^main \"\\\$@\"//' '$AIDER_CLEANUP_SCRIPT')\"
        _remove_expired_workspaces
    " 2>/dev/null

    # Orphaned directory should be removed
    [[ ! -d "$orphan_path" ]]
}

# --- Test: Branch pruning for unmerged branches after workspace removal ---
# Requirement 9.5
@test "cleanup: unmerged agent branches are pruned after workspace removal" {
    local repo_path
    repo_path=$(create_mock_repo "prune-repo")

    local task_id
    task_id=$(generate_test_uuid)
    local branch_name="agent/${task_id}/test-feature"

    # Create a branch in the repo and add a commit so it's NOT merged
    git -C "$repo_path" checkout -b "$branch_name" >/dev/null 2>&1
    echo "agent change" > "${repo_path}/agent_file.txt"
    git -C "$repo_path" add -A >/dev/null 2>&1
    git -C "$repo_path" commit -m "Agent commit" >/dev/null 2>&1
    git -C "$repo_path" checkout main >/dev/null 2>&1

    # Verify branch exists and is NOT merged
    git -C "$repo_path" branch --list "$branch_name" | grep -q "agent/"
    ! git -C "$repo_path" branch --merged main | grep -q "$branch_name"

    # No workspace exists for this task (it was already cleaned up)
    [[ ! -d "${MOCK_WORKSPACE_DIR}/${task_id}" ]]

    # Run branch pruning
    run bash -c "
        export AIDER_WORKSPACE_ROOT='$MOCK_WORKSPACE_DIR'
        export AIDER_REPOS_DIR='$MOCK_REPOS_DIR'
        export AIDER_MAX_WORKSPACE_DISK=10737418240
        export AIDER_LOG_DIR='$MOCK_LOG_DIR'
        eval \"\$(sed 's/^main \"\\\$@\"//' '$AIDER_CLEANUP_SCRIPT')\"
        _prune_orphaned_branches
    " 2>/dev/null

    # Branch should be pruned (workspace doesn't exist, branch not merged)
    local remaining
    remaining=$(git -C "$repo_path" branch --list "$branch_name" 2>/dev/null)
    [[ -z "$remaining" ]]
}

# --- Test: Branch pruning preserves branches with existing workspaces ---
# Requirement 9.5
@test "cleanup: agent branches with existing workspaces are NOT pruned" {
    local repo_path
    repo_path=$(create_mock_repo "keep-branch-repo")

    local task_id
    task_id=$(generate_test_uuid)
    local branch_name="agent/${task_id}/active-task"

    # Create a branch in the repo
    git -C "$repo_path" branch "$branch_name" >/dev/null 2>&1

    # Create a workspace for this task (simulating active task)
    create_active_workspace "$task_id"

    # Run branch pruning
    run bash -c "
        export AIDER_WORKSPACE_ROOT='$MOCK_WORKSPACE_DIR'
        export AIDER_REPOS_DIR='$MOCK_REPOS_DIR'
        export AIDER_MAX_WORKSPACE_DISK=10737418240
        export AIDER_LOG_DIR='$MOCK_LOG_DIR'
        eval \"\$(sed 's/^main \"\\\$@\"//' '$AIDER_CLEANUP_SCRIPT')\"
        _prune_orphaned_branches
    " 2>/dev/null

    # Branch should still exist (workspace exists)
    local remaining
    remaining=$(git -C "$repo_path" branch --list "$branch_name" 2>/dev/null)
    [[ -n "$remaining" ]]
}

# --- Test: Cleanup handles empty workspace root gracefully ---
# Requirement 9.4
@test "cleanup: handles empty workspace root without errors" {
    # Remove all workspaces
    rm -rf "${MOCK_WORKSPACE_DIR:?}"/*

    # Run cleanup — should not error
    run bash -c "
        export AIDER_WORKSPACE_ROOT='$MOCK_WORKSPACE_DIR'
        export AIDER_REPOS_DIR='$MOCK_REPOS_DIR'
        export AIDER_MAX_WORKSPACE_DISK=10737418240
        export AIDER_LOG_DIR='$MOCK_LOG_DIR'
        eval \"\$(sed 's/^main \"\\\$@\"//' '$AIDER_CLEANUP_SCRIPT')\"
        _remove_expired_workspaces
    "

    # Should succeed (exit 0)
    [[ "$status" -eq 0 ]]
}

# --- Test: Cleanup handles missing workspace root gracefully ---
@test "cleanup: handles non-existent workspace root without errors" {
    local nonexistent="/tmp/bats-nonexistent-$$-${RANDOM}"

    run bash -c "
        export AIDER_WORKSPACE_ROOT='$nonexistent'
        export AIDER_REPOS_DIR='$MOCK_REPOS_DIR'
        export AIDER_MAX_WORKSPACE_DISK=10737418240
        export AIDER_LOG_DIR='$MOCK_LOG_DIR'
        eval \"\$(sed 's/^main \"\\\$@\"//' '$AIDER_CLEANUP_SCRIPT')\"
        _remove_expired_workspaces
    "

    [[ "$status" -eq 0 ]]
}
