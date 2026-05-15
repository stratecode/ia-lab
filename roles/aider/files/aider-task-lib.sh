# aider-task-lib.sh — Pure utility functions for the aider-task wrapper script
# Deployed to /usr/local/lib/aider-task-lib.sh by Ansible (mode 0644, root:root)
# Source this file; do not execute directly.

# Environment defaults (overridden by /etc/aider/environment when sourced)
: "${AIDER_REPOS_DIR:=/srv/ai-lab/repos}"
: "${AIDER_PROTECTED_BRANCHES:=main,master,develop}"

# ─── generate_branch_slug ───────────────────────────────────────────────────
# Converts a description string into a branch-safe slug.
#
# Rules:
#   - Lowercase only
#   - Only alphanumeric characters and hyphens
#   - No consecutive hyphens
#   - No leading or trailing hyphens
#   - Maximum 50 characters
#   - Returns "task" if the result is empty after processing
#
# Usage: generate_branch_slug "Some Task Description"
# Output: slug printed to stdout
generate_branch_slug() {
    local description="${1:-}"
    local slug

    # Convert to lowercase
    slug="${description,,}"

    # Replace spaces and underscores with hyphens
    slug="${slug//[ _]/-}"

    # Remove all characters that are not alphanumeric or hyphens
    slug=$(printf '%s' "$slug" | tr -cd 'a-z0-9-')

    # Collapse consecutive hyphens into one
    slug=$(printf '%s' "$slug" | sed 's/-\{2,\}/-/g')

    # Strip leading hyphens
    slug="${slug#"${slug%%[!-]*}"}"

    # Strip trailing hyphens
    slug="${slug%"${slug##*[!-]}"}"

    # Truncate to 50 characters
    slug="${slug:0:50}"

    # Strip trailing hyphens again (truncation may expose one)
    slug="${slug%"${slug##*[!-]}"}"

    # Fallback to "task" if empty
    if [[ -z "$slug" ]]; then
        slug="task"
    fi

    printf '%s' "$slug"
}

# ─── validate_uuid ──────────────────────────────────────────────────────────
# Validates that a string is a valid UUID v4 format.
# UUID v4: 8-4-4-4-12 hex digits, version nibble is 4, variant bits are 8/9/a/b.
#
# Usage: validate_uuid "550e8400-e29b-41d4-a716-446655440000"
# Returns: 0 if valid, 1 if invalid
validate_uuid() {
    local uuid="${1:-}"
    local pattern='^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'

    if [[ "${uuid,,}" =~ $pattern ]]; then
        return 0
    fi
    return 1
}

# ─── validate_params ────────────────────────────────────────────────────────
# Validates task parameters: task-id, repository name, and description.
#
# Checks:
#   - task_id is a valid UUID v4
#   - repo_name corresponds to an existing directory under AIDER_REPOS_DIR
#   - description is non-empty and at most 10,000 characters
#
# Usage: validate_params "$task_id" "$repo_name" "$description"
# Output: error message to stdout on failure
# Returns: 0 if all valid, 1 if any validation fails
validate_params() {
    local task_id="${1:-}"
    local repo_name="${2:-}"
    local description="${3:-}"

    # Validate task-id
    if [[ -z "$task_id" ]]; then
        printf 'validation_error: task-id is required'
        return 1
    fi
    if ! validate_uuid "$task_id"; then
        printf 'validation_error: task-id is not a valid UUID v4'
        return 1
    fi

    # Validate repository name
    if [[ -z "$repo_name" ]]; then
        printf 'validation_error: repository name is required'
        return 1
    fi
    if [[ ! -d "${AIDER_REPOS_DIR}/${repo_name}" ]]; then
        printf 'validation_error: repository "%s" not found in %s' "$repo_name" "$AIDER_REPOS_DIR"
        return 1
    fi

    # Validate description
    if [[ -z "$description" ]]; then
        printf 'validation_error: description is required'
        return 1
    fi
    if (( ${#description} > 10000 )); then
        printf 'validation_error: description exceeds 10000 characters (%d)' "${#description}"
        return 1
    fi

    return 0
}

# ─── format_commit_message ──────────────────────────────────────────────────
# Generates a structured commit message in the format:
#   agent(<task-id>): <description>
#
# The description portion is truncated to at most 72 characters.
#
# Usage: format_commit_message "$task_id" "$description"
# Output: formatted commit message to stdout
format_commit_message() {
    local task_id="${1:-}"
    local description="${2:-}"
    local prefix="agent(${task_id}): "
    local max_desc_len=72
    local truncated_desc

    # Truncate description to max 72 characters
    truncated_desc="${description:0:$max_desc_len}"

    printf '%s%s' "$prefix" "$truncated_desc"
}

# ─── check_disk_quota ────────────────────────────────────────────────────────
# Validates workspace size, artifact size, and log size against configured limits.
#
# All values are in bytes.
# Returns 0 if all within limits, 1 if any exceeded.
# Outputs to stdout which quota was exceeded.
#
# Usage: check_disk_quota workspace_size artifact_size log_size max_workspace max_artifact max_log
check_disk_quota() {
    local workspace_size="${1:-0}"
    local artifact_size="${2:-0}"
    local log_size="${3:-0}"
    local max_workspace="${4:-0}"
    local max_artifact="${5:-0}"
    local max_log="${6:-0}"
    local exceeded=0

    if (( max_workspace > 0 && workspace_size > max_workspace )); then
        printf 'quota_exceeded: workspace size %d exceeds limit %d\n' "$workspace_size" "$max_workspace"
        exceeded=1
    fi

    if (( max_artifact > 0 && artifact_size > max_artifact )); then
        printf 'quota_exceeded: artifact size %d exceeds limit %d\n' "$artifact_size" "$max_artifact"
        exceeded=1
    fi

    if (( max_log > 0 && log_size > max_log )); then
        printf 'quota_exceeded: log size %d exceeds limit %d\n' "$log_size" "$max_log"
        exceeded=1
    fi

    return $exceeded
}

# ─── check_token_budget ──────────────────────────────────────────────────────
# Validates token usage against budget limits.
#
# Returns 0 if within budget, 1 if any metric exceeds its limit.
# A limit value of 0 means unlimited (no check for that metric).
# Outputs to stdout which budget was exceeded.
#
# Usage: check_token_budget prompt_tokens completion_tokens total_tokens max_prompt max_completion max_total
check_token_budget() {
    local prompt_tokens="${1:-0}"
    local completion_tokens="${2:-0}"
    local total_tokens="${3:-0}"
    local max_prompt="${4:-0}"
    local max_completion="${5:-0}"
    local max_total="${6:-0}"
    local exceeded=0

    if (( max_prompt > 0 && prompt_tokens > max_prompt )); then
        printf 'budget_exceeded: prompt tokens %d exceeds limit %d\n' "$prompt_tokens" "$max_prompt"
        exceeded=1
    fi

    if (( max_completion > 0 && completion_tokens > max_completion )); then
        printf 'budget_exceeded: completion tokens %d exceeds limit %d\n' "$completion_tokens" "$max_completion"
        exceeded=1
    fi

    if (( max_total > 0 && total_tokens > max_total )); then
        printf 'budget_exceeded: total tokens %d exceeds limit %d\n' "$total_tokens" "$max_total"
        exceeded=1
    fi

    return $exceeded
}

# ─── compare_priority ────────────────────────────────────────────────────────
# Compares two priority levels for queue ordering.
#
# Priority levels: critical > high > normal > low
# Outputs to stdout: "higher" if a > b, "lower" if a < b, "equal" if a == b
# Exit code 0 always (comparison is always valid).
#
# Usage: compare_priority priority_a priority_b
compare_priority() {
    local priority_a="${1:-normal}"
    local priority_b="${2:-normal}"

    # Map priority to numeric value
    local -A priority_map=( [critical]=4 [high]=3 [normal]=2 [low]=1 )

    local val_a="${priority_map[$priority_a]:-2}"
    local val_b="${priority_map[$priority_b]:-2}"

    if (( val_a > val_b )); then
        printf 'higher'
    elif (( val_a < val_b )); then
        printf 'lower'
    else
        printf 'equal'
    fi

    return 0
}

# ─── classify_failure ────────────────────────────────────────────────────────
# Maps failure types to retryable/non_retryable classification.
#
# Retryable: inference_failure, queue_timeout, timeout
# Non-retryable: validation_failure, permission_violation, repository_error,
#                scope_violation, quota_exceeded, sandbox_failure
#
# Outputs to stdout:
#   Line 1: "retryable" or "non_retryable"
#   Line 2: "true" or "false" (retry_eligible boolean)
# Returns 0 if known type, 1 if unknown type.
#
# Usage: classify_failure failure_type
classify_failure() {
    local failure_type="${1:-}"

    case "$failure_type" in
        inference_failure|queue_timeout|timeout)
            printf 'retryable\ntrue\n'
            return 0
            ;;
        validation_failure|permission_violation|repository_error|scope_violation|quota_exceeded|sandbox_failure)
            printf 'non_retryable\nfalse\n'
            return 0
            ;;
        *)
            printf 'non_retryable\nfalse\n'
            return 1
            ;;
    esac
}

# ─── validate_metadata ───────────────────────────────────────────────────────
# Validates JSON metadata fields.
#
# Checks (when field is present):
#   - correlation_id: must be valid UUID v4
#   - execution_id: must be valid UUID v4
#   - parent_task_id (if not null): must be valid UUID v4
#   - task_type: must be one of: coding, refactor, bugfix, test, documentation
#   - priority: must be one of: low, normal, high, critical
#   - risk_level: must be one of: low, medium, high
#
# Returns 0 if valid, 1 if invalid with error message to stdout.
# Uses jq for JSON parsing.
#
# Usage: validate_metadata "$json_string"
validate_metadata() {
    local json_string="${1:-}"

    # Check that input is valid JSON
    if ! printf '%s' "$json_string" | jq empty 2>/dev/null; then
        printf 'validation_error: invalid JSON'
        return 1
    fi

    local value

    # Validate correlation_id (if present)
    value=$(printf '%s' "$json_string" | jq -r '.correlation_id // empty')
    if [[ -n "$value" ]]; then
        if ! validate_uuid "$value"; then
            printf 'validation_error: correlation_id is not a valid UUID v4'
            return 1
        fi
    fi

    # Validate execution_id (if present)
    value=$(printf '%s' "$json_string" | jq -r '.execution_id // empty')
    if [[ -n "$value" ]]; then
        if ! validate_uuid "$value"; then
            printf 'validation_error: execution_id is not a valid UUID v4'
            return 1
        fi
    fi

    # Validate parent_task_id (if present and not null)
    value=$(printf '%s' "$json_string" | jq -r 'if .parent_task_id == null then "" else (.parent_task_id // empty) end')
    if [[ -n "$value" ]]; then
        if ! validate_uuid "$value"; then
            printf 'validation_error: parent_task_id is not a valid UUID v4'
            return 1
        fi
    fi

    # Validate task_type (if present)
    value=$(printf '%s' "$json_string" | jq -r '.task_type // empty')
    if [[ -n "$value" ]]; then
        case "$value" in
            coding|refactor|bugfix|test|documentation) ;;
            *)
                printf 'validation_error: task_type "%s" is not valid (must be: coding, refactor, bugfix, test, documentation)' "$value"
                return 1
                ;;
        esac
    fi

    # Validate priority (if present)
    value=$(printf '%s' "$json_string" | jq -r '.priority // empty')
    if [[ -n "$value" ]]; then
        case "$value" in
            low|normal|high|critical) ;;
            *)
                printf 'validation_error: priority "%s" is not valid (must be: low, normal, high, critical)' "$value"
                return 1
                ;;
        esac
    fi

    # Validate risk_level (if present)
    value=$(printf '%s' "$json_string" | jq -r '.risk_level // empty')
    if [[ -n "$value" ]]; then
        case "$value" in
            low|medium|high) ;;
            *)
                printf 'validation_error: risk_level "%s" is not valid (must be: low, medium, high)' "$value"
                return 1
                ;;
        esac
    fi

    return 0
}

# ─── filter_scope_paths ──────────────────────────────────────────────────────
# Filters a file list to only those within configured scope paths.
#
# file_list: newline-separated list of file paths
# scope_paths: comma-separated list of path prefixes (e.g., "src/,tests/")
#
# Returns filtered list to stdout (only files within at least one scope path).
# If scope_paths is empty, returns all files (no filtering).
# Exit code 0 always.
#
# Usage: filter_scope_paths "$file_list" "$scope_paths"
filter_scope_paths() {
    local file_list="${1:-}"
    local scope_paths="${2:-}"

    # If scope_paths is empty, return all files
    if [[ -z "$scope_paths" ]]; then
        printf '%s' "$file_list"
        return 0
    fi

    # Split scope_paths by comma into an array
    local -a scopes
    IFS=',' read -ra scopes <<< "$scope_paths"

    # Filter files: keep only those matching at least one scope prefix
    local file
    local scope
    local matched
    local first=1

    while IFS= read -r file; do
        [[ -z "$file" ]] && continue
        matched=0
        for scope in "${scopes[@]}"; do
            # Trim whitespace from scope
            scope="${scope#"${scope%%[![:space:]]*}"}"
            scope="${scope%"${scope##*[![:space:]]}"}"
            [[ -z "$scope" ]] && continue
            if [[ "$file" == "$scope"* ]]; then
                matched=1
                break
            fi
        done
        if (( matched )); then
            if (( first )); then
                printf '%s' "$file"
                first=0
            else
                printf '\n%s' "$file"
            fi
        fi
    done <<< "$file_list"

    return 0
}

# ─── log_event ───────────────────────────────────────────────────────────────
# Emits a structured JSON log entry appended to the specified log file.
#
# Fields: timestamp (ISO 8601), event_type, message, plus any extra_json merged.
# Uses jq for JSON construction if available, falls back to printf.
#
# Usage: log_event "/var/log/aider/task.json" "stage_change" "entering workspace_created" '{"task_id":"..."}'
# Arguments:
#   $1 - log_file: path to the log file (appended)
#   $2 - event_type: type of event (e.g., "stage_change", "error", "info")
#   $3 - message: human-readable message
#   $4 - extra_json: optional JSON object string to merge into the entry
log_event() {
    local log_file="${1:-}"
    local event_type="${2:-}"
    local message="${3:-}"
    local extra_json="${4:-}"
    local timestamp
    local entry

    timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

    if command -v jq &>/dev/null; then
        # Build base JSON object
        entry=$(jq -n \
            --arg ts "$timestamp" \
            --arg evt "$event_type" \
            --arg msg "$message" \
            '{timestamp: $ts, event: $evt, message: $msg}')

        # Merge extra_json fields if provided and non-empty
        if [[ -n "$extra_json" ]]; then
            entry=$(printf '%s' "$entry" | jq --argjson extra "$extra_json" '. + $extra')
        fi
    else
        # Fallback: construct JSON manually via printf
        # Escape double quotes in message for safe JSON embedding
        local escaped_message="${message//\\/\\\\}"
        escaped_message="${escaped_message//\"/\\\"}"
        local escaped_event="${event_type//\\/\\\\}"
        escaped_event="${escaped_event//\"/\\\"}"

        if [[ -n "$extra_json" ]]; then
            # Strip leading { and trailing } from extra_json, then append fields
            local extra_fields="${extra_json#\{}"
            extra_fields="${extra_fields%\}}"
            entry=$(printf '{"timestamp":"%s","event":"%s","message":"%s",%s}' \
                "$timestamp" "$escaped_event" "$escaped_message" "$extra_fields")
        else
            entry=$(printf '{"timestamp":"%s","event":"%s","message":"%s"}' \
                "$timestamp" "$escaped_event" "$escaped_message")
        fi
    fi

    # Append the JSON line to the log file
    printf '%s\n' "$entry" >> "$log_file"
}

# ─── log_stage_transition ────────────────────────────────────────────────────
# Records a stage transition entry in the execution log file.
#
# Calculates duration_seconds from started_at and ended_at timestamps.
# Appends the stage object to the "stages" array in the JSON log file.
#
# Usage: log_stage_transition "/var/log/aider/task.json" "uuid" "workspace_created" "2024-01-15T10:30:00Z" "2024-01-15T10:30:05Z"
# Arguments:
#   $1 - log_file: path to the execution log JSON file
#   $2 - task_id: the task UUID
#   $3 - stage_name: name of the stage (e.g., "pending", "workspace_created")
#   $4 - started_at: ISO 8601 timestamp when stage started
#   $5 - ended_at: ISO 8601 timestamp when stage ended
log_stage_transition() {
    local log_file="${1:-}"
    local task_id="${2:-}"
    local stage_name="${3:-}"
    local started_at="${4:-}"
    local ended_at="${5:-}"
    local duration_seconds=0

    # Calculate duration in seconds from ISO 8601 timestamps
    local start_epoch end_epoch
    if date --version &>/dev/null 2>&1; then
        # GNU date
        start_epoch=$(date -d "$started_at" +%s 2>/dev/null || echo 0)
        end_epoch=$(date -d "$ended_at" +%s 2>/dev/null || echo 0)
    else
        # macOS/BSD date
        start_epoch=$(date -j -f "%Y-%m-%dT%H:%M:%SZ" "$started_at" +%s 2>/dev/null || echo 0)
        end_epoch=$(date -j -f "%Y-%m-%dT%H:%M:%SZ" "$ended_at" +%s 2>/dev/null || echo 0)
    fi

    if (( end_epoch > 0 && start_epoch > 0 )); then
        duration_seconds=$(( end_epoch - start_epoch ))
    fi

    if command -v jq &>/dev/null && [[ -f "$log_file" ]]; then
        # Build the stage entry
        local stage_entry
        stage_entry=$(jq -n \
            --arg name "$stage_name" \
            --arg sa "$started_at" \
            --arg ea "$ended_at" \
            --argjson dur "$duration_seconds" \
            '{name: $name, started_at: $sa, ended_at: $ea, duration_seconds: $dur}')

        # Append to the stages array in the log file
        # If the file has a stages array, append to it; otherwise create one
        local updated
        updated=$(jq --argjson stage "$stage_entry" \
            'if .stages then .stages += [$stage] else .stages = [$stage] end' \
            "$log_file" 2>/dev/null)

        if [[ -n "$updated" ]]; then
            printf '%s\n' "$updated" > "$log_file"
        else
            # File doesn't contain valid JSON yet; create initial structure
            jq -n \
                --arg tid "$task_id" \
                --argjson stage "$stage_entry" \
                '{task_id: $tid, stages: [$stage]}' > "$log_file"
        fi
    else
        # Fallback without jq: append a stage log event line
        local entry
        entry=$(printf '{"name":"%s","started_at":"%s","ended_at":"%s","duration_seconds":%d}' \
            "$stage_name" "$started_at" "$ended_at" "$duration_seconds")
        printf '%s\n' "$entry" >> "$log_file"
    fi
}

# ─── generate_failure_report ─────────────────────────────────────────────────
# Creates a FAILURE_REPORT.md at the workspace path with task failure details.
#
# The error_output is truncated to 5KB (5120 bytes) if longer.
# The modified_files argument is a newline-separated list of file paths.
#
# Usage: generate_failure_report "/srv/ai-lab/aider/workspaces/uuid" "uuid" "my-repo" "agent/uuid/slug" "executing" "inference_failure" "error text" "file1\nfile2"
# Arguments:
#   $1 - workspace_path: path to the workspace directory
#   $2 - task_id: the task UUID
#   $3 - repo_name: repository name
#   $4 - branch_name: the task branch name
#   $5 - failed_stage: the stage where failure occurred
#   $6 - failure_type: classification of the failure
#   $7 - error_output: raw error output (will be truncated to 5KB)
#   $8 - modified_files: newline-separated list of modified files
generate_failure_report() {
    local workspace_path="${1:-}"
    local task_id="${2:-}"
    local repo_name="${3:-}"
    local branch_name="${4:-}"
    local failed_stage="${5:-}"
    local failure_type="${6:-}"
    local error_output="${7:-}"
    local modified_files="${8:-}"
    local timestamp
    local report_path

    timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    report_path="${workspace_path}/FAILURE_REPORT.md"

    # Truncate error_output to 5KB (5120 bytes) if longer
    if (( ${#error_output} > 5120 )); then
        error_output="${error_output: -5120}"
    fi

    # Build the modified files list as markdown
    local files_list=""
    if [[ -n "$modified_files" ]]; then
        while IFS= read -r file; do
            if [[ -n "$file" ]]; then
                files_list="${files_list}- ${file}"$'\n'
            fi
        done <<< "$modified_files"
    else
        files_list="No files modified."$'\n'
    fi

    # Write the failure report
    cat > "$report_path" << EOF
# Task Failure Report

- **Task ID**: ${task_id}
- **Repository**: ${repo_name}
- **Branch**: ${branch_name}
- **Failed Stage**: ${failed_stage}
- **Failure Type**: ${failure_type}
- **Timestamp**: ${timestamp}

## Error Output

\`\`\`
${error_output}
\`\`\`

## Modified Files

${files_list}
## Execution Context

- Duration: <seconds> (placeholder, filled by caller)
- Token Usage: <if available> (placeholder)
- Exit Code: <exit code> (placeholder)
EOF
}

# ─── resolve_execution_mode ─────────────────────────────────────────────────
# Resolves the effective execution mode using precedence:
#   CLI parameter > repository-level setting > global default
#
# Valid modes: autonomous, supervised, dry-run
# If a level is empty/unset, the next lower level applies.
#
# Usage: resolve_execution_mode "$cli_mode" "$repo_mode" "$global_mode"
# Output: effective mode printed to stdout
# Returns: 0 if a valid mode is resolved, 1 if no valid mode found
resolve_execution_mode() {
    local cli_mode="${1:-}"
    local repo_mode="${2:-}"
    local global_mode="${3:-}"
    local valid_modes="autonomous supervised dry-run"
    local effective=""

    # CLI overrides everything
    if [[ -n "$cli_mode" ]]; then
        effective="$cli_mode"
    elif [[ -n "$repo_mode" ]]; then
        effective="$repo_mode"
    elif [[ -n "$global_mode" ]]; then
        effective="$global_mode"
    fi

    # Validate the resolved mode
    if [[ -z "$effective" ]]; then
        printf 'error: no execution mode resolved at any level'
        return 1
    fi

    local mode
    for mode in $valid_modes; do
        if [[ "$effective" == "$mode" ]]; then
            printf '%s' "$effective"
            return 0
        fi
    done

    printf 'error: invalid execution mode "%s" (valid: autonomous, supervised, dry-run)' "$effective"
    return 1
}

# ─── validate_branch_name ───────────────────────────────────────────────────
# Validates that a branch name is allowed for agent operations.
#
# Rules:
#   - Must start with prefix "agent/"
#   - Must NOT match any entry in AIDER_PROTECTED_BRANCHES (comma-separated)
#
# Usage: validate_branch_name "agent/task-id/my-feature"
# Output: error message to stdout on failure
# Returns: 0 if valid, 1 if invalid
validate_branch_name() {
    local branch_name="${1:-}"
    local protected_branches="${AIDER_PROTECTED_BRANCHES:-main,master,develop}"

    # Check agent/ prefix
    if [[ "$branch_name" != agent/* ]]; then
        printf 'branch_error: branch "%s" does not start with required prefix "agent/"' "$branch_name"
        return 1
    fi

    # Check against protected branches list
    local IFS=','
    local protected
    for protected in $protected_branches; do
        # Trim whitespace
        protected="${protected#"${protected%%[![:space:]]*}"}"
        protected="${protected%"${protected##*[![:space:]]}"}"
        if [[ "$branch_name" == "$protected" ]]; then
            printf 'branch_error: branch "%s" is in the protected branches list' "$branch_name"
            return 1
        fi
    done

    return 0
}

# ─── validate_file_policy ───────────────────────────────────────────────────
# Validates modified files against the file modification policy.
#
# Checks (in order):
#   1. File count vs max_files (0 = unlimited)
#   2. Line count vs max_lines (0 = unlimited, requires git diff --stat in workspace)
#   3. Forbidden paths (comma-separated glob patterns)
#   4. Forbidden extensions (comma-separated, e.g. ".pem,.key,.p12")
#
# Usage: validate_file_policy "$modified_files" "$max_files" "$max_lines" "$forbidden_paths" "$forbidden_extensions"
#   modified_files: newline-separated list of modified file paths
#   max_files: maximum number of modified files allowed (0 = unlimited)
#   max_lines: maximum total lines changed (0 = unlimited)
#   forbidden_paths: comma-separated glob patterns (e.g., "*.env,secrets/,.git/")
#   forbidden_extensions: comma-separated extensions (e.g., ".pem,.key,.p12")
#
# Output: violation description to stdout on failure
# Returns: 0 if policy passes, 1 if violated
validate_file_policy() {
    local modified_files="${1:-}"
    local max_files="${2:-0}"
    local max_lines="${3:-0}"
    local forbidden_paths_pattern="${4:-}"
    local forbidden_extensions="${5:-}"

    # If no files modified, policy passes trivially
    if [[ -z "$modified_files" ]]; then
        return 0
    fi

    # Count modified files
    local file_count
    file_count=$(printf '%s\n' "$modified_files" | grep -c .)

    # Check file count
    if (( max_files > 0 && file_count > max_files )); then
        printf 'policy_violation: file count %d exceeds maximum %d' "$file_count" "$max_files"
        return 1
    fi

    # Check line count (requires git diff --stat output parsing in the workspace)
    if (( max_lines > 0 )); then
        local total_lines=0
        if command -v git &>/dev/null && git rev-parse --is-inside-work-tree &>/dev/null 2>&1; then
            total_lines=$(git diff --stat HEAD~1 2>/dev/null | tail -1 | grep -oP '\d+(?= insertion)' || echo 0)
            local deletions
            deletions=$(git diff --stat HEAD~1 2>/dev/null | tail -1 | grep -oP '\d+(?= deletion)' || echo 0)
            total_lines=$(( total_lines + deletions ))
        fi
        if (( total_lines > max_lines )); then
            printf 'policy_violation: line count %d exceeds maximum %d' "$total_lines" "$max_lines"
            return 1
        fi
    fi

    # Check forbidden paths
    if [[ -n "$forbidden_paths_pattern" ]]; then
        local IFS=','
        local pattern
        for pattern in $forbidden_paths_pattern; do
            # Trim whitespace
            pattern="${pattern#"${pattern%%[![:space:]]*}"}"
            pattern="${pattern%"${pattern##*[![:space:]]}"}"
            [[ -z "$pattern" ]] && continue

            local file
            while IFS= read -r file; do
                [[ -z "$file" ]] && continue
                # Use bash pattern matching (fnmatch-style)
                # Check if file matches the glob pattern
                # shellcheck disable=SC2254
                case "$file" in
                    $pattern)
                        printf 'policy_violation: file "%s" matches forbidden path pattern "%s"' "$file" "$pattern"
                        return 1
                        ;;
                esac
                # Also check if any path component matches directory patterns (ending with /)
                if [[ "$pattern" == */ ]]; then
                    if [[ "$file" == ${pattern}* ]]; then
                        printf 'policy_violation: file "%s" is under forbidden path "%s"' "$file" "$pattern"
                        return 1
                    fi
                fi
            done <<< "$modified_files"
        done
    fi

    # Check forbidden extensions
    if [[ -n "$forbidden_extensions" ]]; then
        local IFS=','
        local ext
        for ext in $forbidden_extensions; do
            # Trim whitespace
            ext="${ext#"${ext%%[![:space:]]*}"}"
            ext="${ext%"${ext##*[![:space:]]}"}"
            [[ -z "$ext" ]] && continue

            local file
            while IFS= read -r file; do
                [[ -z "$file" ]] && continue
                if [[ "$file" == *"$ext" ]]; then
                    printf 'policy_violation: file "%s" has forbidden extension "%s"' "$file" "$ext"
                    return 1
                fi
            done <<< "$modified_files"
        done
    fi

    return 0
}

# ─── check_access_mode ──────────────────────────────────────────────────────
# Enforces repository access mode restrictions.
#
# Access modes:
#   - full-edit: allows both read and write operations
#   - review-only: allows read, denies write
#   - readonly: allows read, denies write
#
# Usage: check_access_mode "$access_mode" "$operation"
#   access_mode: full-edit, review-only, or readonly
#   operation: write or read
#
# Output: error message to stdout on denial
# Returns: 0 if allowed, 1 if denied
check_access_mode() {
    local access_mode="${1:-full-edit}"
    local operation="${2:-read}"

    # Read operations are always allowed
    if [[ "$operation" == "read" ]]; then
        return 0
    fi

    # Write operations require full-edit mode
    if [[ "$operation" == "write" ]]; then
        if [[ "$access_mode" == "full-edit" ]]; then
            return 0
        fi
        printf 'permission_violation: write operation denied — repository access mode is "%s"' "$access_mode"
        return 1
    fi

    # Unknown operation type
    printf 'permission_violation: unknown operation "%s"' "$operation"
    return 1
}
