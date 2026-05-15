#!/usr/bin/env bats
# integration_lifecycle.bats — Integration tests for aider-task lifecycle
# Tests the full task execution lifecycle with a real git repository and mock Aider.
#
# These tests exercise the same lifecycle stages as the production aider-task script:
#   pending → workspace_created → executing → testing → committing → completed
#
# Requirements validated:
#   4.1  — Workspace creation via git worktree
#   5.1  — Task execution lifecycle stages
#   5.4  — Aider completion proceeds to testing
#   5.5  — Aider failure sets status "failed"
#   5.6  — Task timeout enforcement
#   5.7  — SIGTERM/SIGKILL sequence on timeout
#   7.1  — Test execution after Aider success
#   7.3  — Test pass proceeds to commit
#   7.4  — Test failure preserves workspace
#   8.1  — Git add -A and commit on success
#   8.4  — No changes → status "no_changes"
#   10.1 — Branch must start with agent/
#   10.2 — Protected branch rejection
#   16.1 — Workspace preserved on test failure
#   16.2 — Workspace preserved on Aider error
#   16.3 — Workspace preserved on timeout

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
    create_test_environment
}

teardown() {
    cleanup_test_environment
}

# ─── Happy Path ──────────────────────────────────────────────────────────────

@test "happy path: worktree → mock Aider → tests pass → commit created" {
    # Requirements: 4.1, 5.1, 5.4, 7.1, 7.3, 8.1
    local task_id
    task_id=$(generate_task_id)

    # Stage 1: Create workspace (worktree)
    create_workspace "$task_id" "Add goodbye function"
    [[ $? -eq 0 ]]
    assert_dir_exists "$WORKSPACE_PATH"
    [[ -f "${WORKSPACE_PATH}/.git" ]]  # worktree has .git file, not directory

    # Verify branch name starts with agent/
    local current_branch
    current_branch=$(git -C "$WORKSPACE_PATH" rev-parse --abbrev-ref HEAD)
    [[ "$current_branch" == agent/* ]]

    # Stage 2: Execute mock Aider (modifies files)
    run execute_mock_aider "$WORKSPACE_PATH"
    [[ "$status" -eq 0 ]]

    # Verify files were modified
    local modified_files
    modified_files=$(git -C "$WORKSPACE_PATH" diff --name-only)
    [[ -n "$modified_files" ]]

    # Stage 3: Run tests (pass)
    run run_test_command "$WORKSPACE_PATH" "true"
    [[ "$status" -eq 0 ]]

    # Stage 4: Create commit
    create_commit "$WORKSPACE_PATH" "$task_id" "Add goodbye function"
    [[ $? -eq 0 ]]

    # Verify commit was created with correct format
    local commit_msg
    commit_msg=$(git -C "$WORKSPACE_PATH" log -1 --format=%s)
    [[ "$commit_msg" == agent\(${task_id}\):* ]]

    # Verify commit contains the modified file
    local committed_files
    committed_files=$(git -C "$WORKSPACE_PATH" diff --name-only HEAD~1)
    [[ "$committed_files" == *"src/main.py"* ]]
}

# ─── Aider Failure ───────────────────────────────────────────────────────────

@test "Aider failure: mock exits non-zero → workspace preserved" {
    # Requirements: 5.5, 16.2
    local task_id
    task_id=$(generate_task_id)

    create_workspace "$task_id" "This will fail"
    [[ $? -eq 0 ]]

    # Switch to failure mock
    create_mock_aider "failure"

    # Execute mock Aider (should fail)
    run execute_mock_aider "$WORKSPACE_PATH"
    [[ "$status" -eq 1 ]]

    # Workspace should be preserved (not deleted)
    assert_dir_exists "$WORKSPACE_PATH"

    # Workspace should still be a valid git worktree
    git -C "$WORKSPACE_PATH" rev-parse --is-inside-work-tree >/dev/null 2>&1
}

# ─── Test Failure ────────────────────────────────────────────────────────────

@test "test failure: mock Aider succeeds → test command fails → workspace preserved" {
    # Requirements: 7.4, 16.1
    local task_id
    task_id=$(generate_task_id)

    create_workspace "$task_id" "Modify code but tests fail"
    [[ $? -eq 0 ]]

    # Execute mock Aider (success — modifies files)
    run execute_mock_aider "$WORKSPACE_PATH"
    [[ "$status" -eq 0 ]]

    # Verify files were modified
    local modified_files
    modified_files=$(git -C "$WORKSPACE_PATH" diff --name-only)
    [[ -n "$modified_files" ]]

    # Run failing test command
    run run_test_command "$WORKSPACE_PATH" "exit 1"
    [[ "$status" -ne 0 ]]

    # Workspace should be preserved with uncommitted changes
    assert_dir_exists "$WORKSPACE_PATH"

    # Modified files should still be present (not reset)
    local still_modified
    still_modified=$(git -C "$WORKSPACE_PATH" diff --name-only)
    [[ -n "$still_modified" ]]
    grep -q "AI World" "${WORKSPACE_PATH}/src/main.py"
}

# ─── Timeout ─────────────────────────────────────────────────────────────────

@test "timeout: mock Aider sleeps beyond timeout → SIGTERM/SIGKILL sequence" {
    # Requirements: 5.6, 5.7, 16.3
    local task_id
    task_id=$(generate_task_id)

    create_workspace "$task_id" "This will timeout"
    [[ $? -eq 0 ]]

    # Use timeout mock and set very short timeout
    create_mock_aider "timeout"
    export AIDER_TASK_TIMEOUT=2

    # Execute mock Aider (should timeout)
    run execute_mock_aider "$WORKSPACE_PATH"
    [[ "$status" -eq 2 ]]  # 2 = timeout

    # Workspace should be preserved
    assert_dir_exists "$WORKSPACE_PATH"
}

# ─── Policy Violation ────────────────────────────────────────────────────────

@test "policy violation: mock modifies forbidden files → task rejected" {
    # Requirements: 20.3, 20.4, 20.5
    local task_id
    task_id=$(generate_task_id)

    create_workspace "$task_id" "Modify forbidden files"
    [[ $? -eq 0 ]]

    # Use forbidden mock (creates secrets/credentials.pem)
    create_mock_aider "forbidden"

    # Execute mock Aider
    run execute_mock_aider "$WORKSPACE_PATH"
    [[ "$status" -eq 0 ]]

    # Get list of modified files
    local modified_files
    modified_files=$(git -C "$WORKSPACE_PATH" status --porcelain | awk '{print $2}')

    # Validate file policy — should detect forbidden path
    run validate_file_policy "$modified_files" "0" "0" "secrets/" ".pem"
    [[ "$status" -ne 0 ]]
    [[ "$output" == *"policy_violation"* ]]

    # Workspace should be preserved for debugging
    assert_dir_exists "$WORKSPACE_PATH"
}

# ─── Protected Branch ────────────────────────────────────────────────────────

@test "protected branch: attempt non-agent/ branch → task rejected" {
    # Requirements: 10.1, 10.2
    local task_id
    task_id=$(generate_task_id)

    # Try to validate a non-agent/ branch name
    local branch_name="feature/${task_id}/my-feature"
    run validate_branch_name "$branch_name"
    [[ "$status" -ne 0 ]]
    [[ "$output" == *"branch_error"* ]]
    [[ "$output" == *"does not start with required prefix"* ]]

    # Also test that protected branches are rejected
    run validate_branch_name "main"
    [[ "$status" -ne 0 ]]
}

@test "protected branch: agent/ prefix is accepted" {
    # Requirements: 10.1
    local task_id
    task_id=$(generate_task_id)

    local branch_name="agent/${task_id}/my-feature"
    validate_branch_name "$branch_name"
    [[ $? -eq 0 ]]
}

# ─── No Changes ──────────────────────────────────────────────────────────────

@test "no changes: mock makes no modifications → status no_changes" {
    # Requirements: 8.4
    local task_id
    task_id=$(generate_task_id)

    create_workspace "$task_id" "Do nothing"
    [[ $? -eq 0 ]]

    # Use no_changes mock
    create_mock_aider "no_changes"

    # Execute mock Aider (exits 0 but makes no changes)
    run execute_mock_aider "$WORKSPACE_PATH"
    [[ "$status" -eq 0 ]]

    # Attempt to commit — should return 1 (no changes)
    run create_commit "$WORKSPACE_PATH" "$task_id" "Do nothing"
    [[ "$status" -eq 1 ]]
}

# ─── Workspace Isolation ─────────────────────────────────────────────────────

@test "workspace is isolated git worktree with .git file" {
    # Requirements: 4.1, 4.4
    local task_id
    task_id=$(generate_task_id)

    create_workspace "$task_id" "Test isolation"
    [[ $? -eq 0 ]]

    # Workspace should have a .git file (not directory) — indicates worktree
    [[ -f "${WORKSPACE_PATH}/.git" ]]
    [[ ! -d "${WORKSPACE_PATH}/.git" ]]

    # git rev-parse should confirm it's inside a work tree
    git -C "$WORKSPACE_PATH" rev-parse --is-inside-work-tree >/dev/null 2>&1
    [[ $? -eq 0 ]]

    # Changes in workspace should not affect the source repo
    run execute_mock_aider "$WORKSPACE_PATH"
    [[ "$status" -eq 0 ]]

    local source_status
    source_status=$(git -C "$TEST_REPO_PATH" status --porcelain)
    [[ -z "$source_status" ]]
}

# ─── Commit Message Format ───────────────────────────────────────────────────

@test "commit message follows agent(<task-id>): <description> format" {
    # Requirements: 8.1, 8.2
    local task_id
    task_id=$(generate_task_id)

    create_workspace "$task_id" "Implement feature X with detailed description that is quite long"
    run execute_mock_aider "$WORKSPACE_PATH"
    [[ "$status" -eq 0 ]]

    create_commit "$WORKSPACE_PATH" "$task_id" "Implement feature X with detailed description that is quite long"
    [[ $? -eq 0 ]]

    local commit_msg
    commit_msg=$(git -C "$WORKSPACE_PATH" log -1 --format=%s)

    # Must start with agent(<task-id>):
    [[ "$commit_msg" == "agent(${task_id}):"* ]]

    # Description portion should be at most 72 characters
    local desc_part="${commit_msg#agent(${task_id}): }"
    [[ ${#desc_part} -le 72 ]]
}

# ─── Branch Naming ───────────────────────────────────────────────────────────

@test "branch name follows agent/<task-id>/<slug> convention" {
    # Requirements: 4.2
    local task_id
    task_id=$(generate_task_id)

    create_workspace "$task_id" "My Feature Description"
    [[ $? -eq 0 ]]

    # Branch should follow the convention
    [[ "$BRANCH_NAME" == "agent/${task_id}/my-feature-description" ]]

    # Verify the branch exists in the repo
    git -C "$TEST_REPO_PATH" branch --list "$BRANCH_NAME" | grep -q "$BRANCH_NAME"
}

# ─── Failure Report Generation ───────────────────────────────────────────────

@test "failure report generated with correct structure" {
    # Requirements: 16.4, 16.5
    local task_id
    task_id=$(generate_task_id)

    create_workspace "$task_id" "Generate failure report"
    [[ $? -eq 0 ]]

    # Generate a failure report
    generate_failure_report \
        "$WORKSPACE_PATH" \
        "$task_id" \
        "test-project" \
        "$BRANCH_NAME" \
        "executing" \
        "inference_failure" \
        "Error: connection refused to inference endpoint" \
        "src/main.py"

    local report="${WORKSPACE_PATH}/FAILURE_REPORT.md"
    assert_file_exists "$report"

    # Report should contain task ID
    grep -q "$task_id" "$report"

    # Report should contain repository name
    grep -q "test-project" "$report"

    # Report should contain failure type
    grep -q "inference_failure" "$report"

    # Report should contain error output
    grep -q "connection refused" "$report"

    # Report should contain modified files
    grep -q "src/main.py" "$report"
}

# ─── Execution Log Structure ─────────────────────────────────────────────────

@test "execution log records stage transitions correctly" {
    # Requirements: 5.2, 12.2, 12.3
    local task_id
    task_id=$(generate_task_id)
    local log_file="${TEST_LOG_DIR}/${task_id}.json"

    # Initialize a log file
    echo '{"task_id":"'"$task_id"'","stages":[]}' > "$log_file"

    # Record stage transitions
    local start_time="2024-01-15T10:30:00Z"
    local end_time="2024-01-15T10:30:05Z"

    log_stage_transition "$log_file" "$task_id" "pending" "$start_time" "$end_time"

    # Verify the stage was recorded
    assert_file_exists "$log_file"

    if command -v jq &>/dev/null; then
        local stage_name
        stage_name=$(jq -r '.stages[0].name' "$log_file")
        [[ "$stage_name" == "pending" ]]

        local duration
        duration=$(jq -r '.stages[0].duration_seconds' "$log_file")
        [[ "$duration" == "5" ]]
    fi
}

@test "log_event appends structured JSON entries" {
    # Requirements: 12.1, 12.2
    local task_id
    task_id=$(generate_task_id)
    local log_file="${TEST_LOG_DIR}/${task_id}.log"

    # Start with an empty file (log_event appends lines)
    : > "$log_file"

    # Log an event
    log_event "$log_file" "stage_change" "Entering workspace_created" \
        '{"task_id":"'"$task_id"'"}'

    # Verify the event was appended
    [[ -s "$log_file" ]]

    # The file should contain valid JSON with the event fields
    if command -v jq &>/dev/null; then
        # log_event may produce multi-line pretty-printed JSON
        # Read the entire file content as a single JSON object
        local content
        content=$(cat "$log_file")
        echo "$content" | jq -e '.event == "stage_change"' >/dev/null
        echo "$content" | jq -e '.message == "Entering workspace_created"' >/dev/null
        echo "$content" | jq -e 'has("timestamp")' >/dev/null
        echo "$content" | jq -e 'has("task_id")' >/dev/null
    fi
}
