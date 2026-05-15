#!/usr/bin/env bats
# Feature: agent-runtime, Property 6: File Modification Policy Validation
# Validates: Requirements 20.3, 20.4, 20.5

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
}

# ─── Helper generators ───────────────────────────────────────────────────────

# random_file_list(count)
# Generates a newline-separated list of random file paths.
random_file_list() {
    local count="${1:-$(( (RANDOM % 10) + 1 ))}"
    local result=""
    local i

    for ((i = 0; i < count; i++)); do
        if [[ -n "$result" ]]; then
            result+=$'\n'
        fi
        result+="$(random_file_path)"
    done

    printf '%s' "$result"
}

# random_file_list_with_extension(count, extension)
# Generates file paths that all end with the given extension.
random_file_list_with_extension() {
    local count="${1:-1}"
    local ext="${2:-.pem}"
    local result=""
    local dirs=("src" "lib" "config" "secrets" "deploy" "internal")
    local i

    for ((i = 0; i < count; i++)); do
        if [[ -n "$result" ]]; then
            result+=$'\n'
        fi
        local dir_idx=$(( RANDOM % ${#dirs[@]} ))
        local name_len=$(( (RANDOM % 8) + 3 ))
        local name=""
        local chars="abcdefghijklmnopqrstuvwxyz0123456789"
        local j
        for ((j = 0; j < name_len; j++)); do
            name+="${chars:$(( RANDOM % ${#chars} )):1}"
        done
        result+="${dirs[$dir_idx]}/${name}${ext}"
    done

    printf '%s' "$result"
}

# random_file_list_under_path(count, path_prefix)
# Generates file paths that are all under the given path prefix.
random_file_list_under_path() {
    local count="${1:-1}"
    local prefix="${2:-secrets/}"
    local result=""
    local i

    for ((i = 0; i < count; i++)); do
        if [[ -n "$result" ]]; then
            result+=$'\n'
        fi
        local name_len=$(( (RANDOM % 8) + 3 ))
        local name=""
        local chars="abcdefghijklmnopqrstuvwxyz0123456789"
        local extensions=(".py" ".sh" ".js" ".yml" ".json")
        local j
        for ((j = 0; j < name_len; j++)); do
            name+="${chars:$(( RANDOM % ${#chars} )):1}"
        done
        local ext_idx=$(( RANDOM % ${#extensions[@]} ))
        result+="${prefix}${name}${extensions[$ext_idx]}"
    done

    printf '%s' "$result"
}

# random_safe_file_list(count)
# Generates file paths that won't match common forbidden patterns.
random_safe_file_list() {
    local count="${1:-$(( (RANDOM % 5) + 1 ))}"
    local result=""
    local safe_dirs=("src" "lib" "tests" "docs" "utils")
    local safe_exts=(".py" ".sh" ".js" ".ts" ".md")
    local i

    for ((i = 0; i < count; i++)); do
        if [[ -n "$result" ]]; then
            result+=$'\n'
        fi
        local dir_idx=$(( RANDOM % ${#safe_dirs[@]} ))
        local ext_idx=$(( RANDOM % ${#safe_exts[@]} ))
        local name_len=$(( (RANDOM % 8) + 3 ))
        local name=""
        local chars="abcdefghijklmnopqrstuvwxyz0123456789"
        local j
        for ((j = 0; j < name_len; j++)); do
            name+="${chars:$(( RANDOM % ${#chars} )):1}"
        done
        result+="${safe_dirs[$dir_idx]}/${name}${safe_exts[$ext_idx]}"
    done

    printf '%s' "$result"
}

# ─── Property Tests ──────────────────────────────────────────────────────────

@test "Property 6: File count violation — when file count exceeds max_files, returns non-zero" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Generate a max_files limit between 1 and 10
        local max_files=$(( (RANDOM % 10) + 1 ))
        # Generate a file count that exceeds the limit (max_files + 1 to max_files + 20)
        local file_count=$(( max_files + (RANDOM % 20) + 1 ))

        local files
        files=$(random_safe_file_list "$file_count")

        run validate_file_policy "$files" "$max_files" "0" "" ""
        [[ "$status" -ne 0 ]] || {
            echo "FAILED (iteration $i): expected non-zero exit for $file_count files with max $max_files" >&2
            return 1
        }
        [[ "$output" == *"policy_violation"* ]] || {
            echo "FAILED (iteration $i): expected 'policy_violation' in output, got: '$output'" >&2
            return 1
        }
        [[ "$output" == *"file count"* ]] || {
            echo "FAILED (iteration $i): expected 'file count' in output, got: '$output'" >&2
            return 1
        }
    done
}

@test "Property 6: File count within limit — when file count is at or below max_files, passes file count check" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Generate a max_files limit between 5 and 30
        local max_files=$(( (RANDOM % 26) + 5 ))
        # Generate a file count at or below the limit
        local file_count=$(( (RANDOM % max_files) + 1 ))

        local files
        files=$(random_safe_file_list "$file_count")

        # No forbidden paths/extensions, no line limit — should pass
        run validate_file_policy "$files" "$max_files" "0" "" ""
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): expected zero exit for $file_count files with max $max_files, got status=$status output='$output'" >&2
            return 1
        }
    done
}

@test "Property 6: Forbidden extension violation — files with forbidden extensions are detected" {
    local forbidden_exts=(".pem" ".key" ".p12" ".env" ".secret")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Pick a random forbidden extension
        local ext_idx=$(( RANDOM % ${#forbidden_exts[@]} ))
        local forbidden_ext="${forbidden_exts[$ext_idx]}"

        # Generate some safe files plus one with the forbidden extension
        local safe_count=$(( (RANDOM % 5) + 1 ))
        local safe_files
        safe_files=$(random_safe_file_list "$safe_count")

        local bad_file
        bad_file=$(random_file_list_with_extension 1 "$forbidden_ext")

        local all_files="${safe_files}"$'\n'"${bad_file}"

        run validate_file_policy "$all_files" "0" "0" "" "$forbidden_ext"
        [[ "$status" -ne 0 ]] || {
            echo "FAILED (iteration $i): expected non-zero exit for file with extension '$forbidden_ext'" >&2
            echo "  files: $all_files" >&2
            return 1
        }
        [[ "$output" == *"policy_violation"* ]] || {
            echo "FAILED (iteration $i): expected 'policy_violation' in output, got: '$output'" >&2
            return 1
        }
        [[ "$output" == *"forbidden extension"* ]] || {
            echo "FAILED (iteration $i): expected 'forbidden extension' in output, got: '$output'" >&2
            return 1
        }
    done
}

@test "Property 6: Forbidden path violation — files matching forbidden path globs are detected" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Generate files under a forbidden directory path
        local forbidden_dir
        local dirs=("secrets/" ".git/" "private/" "credentials/" "vault/")
        local dir_idx=$(( RANDOM % ${#dirs[@]} ))
        forbidden_dir="${dirs[$dir_idx]}"

        local bad_files
        bad_files=$(random_file_list_under_path 1 "$forbidden_dir")

        # Add some safe files
        local safe_count=$(( (RANDOM % 5) + 1 ))
        local safe_files
        safe_files=$(random_safe_file_list "$safe_count")

        local all_files="${safe_files}"$'\n'"${bad_files}"

        run validate_file_policy "$all_files" "0" "0" "${forbidden_dir}" ""
        [[ "$status" -ne 0 ]] || {
            echo "FAILED (iteration $i): expected non-zero exit for file under forbidden path '$forbidden_dir'" >&2
            echo "  bad_files: $bad_files" >&2
            echo "  all_files: $all_files" >&2
            return 1
        }
        [[ "$output" == *"policy_violation"* ]] || {
            echo "FAILED (iteration $i): expected 'policy_violation' in output, got: '$output'" >&2
            return 1
        }
    done
}

@test "Property 6: Multiple forbidden extensions — comma-separated extensions all detected" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Create a comma-separated list of forbidden extensions
        local all_exts=(".pem" ".key" ".p12" ".cert" ".secret")
        local ext_count=$(( (RANDOM % 3) + 2 ))
        local forbidden_list=""
        local chosen_ext=""

        for ((j = 0; j < ext_count; j++)); do
            local idx=$(( RANDOM % ${#all_exts[@]} ))
            if [[ -n "$forbidden_list" ]]; then
                forbidden_list+=","
            fi
            forbidden_list+="${all_exts[$idx]}"
            # Remember the last one to use for the bad file
            chosen_ext="${all_exts[$idx]}"
        done

        # Generate safe files plus one with a forbidden extension
        local safe_files
        safe_files=$(random_safe_file_list 3)
        local bad_file
        bad_file=$(random_file_list_with_extension 1 "$chosen_ext")

        local all_files="${safe_files}"$'\n'"${bad_file}"

        run validate_file_policy "$all_files" "0" "0" "" "$forbidden_list"
        [[ "$status" -ne 0 ]] || {
            echo "FAILED (iteration $i): expected non-zero for extension '$chosen_ext' in list '$forbidden_list'" >&2
            echo "  bad_file: $bad_file" >&2
            return 1
        }
        [[ "$output" == *"policy_violation"* ]] || {
            echo "FAILED (iteration $i): expected 'policy_violation' in output, got: '$output'" >&2
            return 1
        }
    done
}

@test "Property 6: No violations — safe files with permissive policy always pass" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local file_count=$(( (RANDOM % 10) + 1 ))
        local max_files=$(( file_count + (RANDOM % 20) + 1 ))

        local files
        files=$(random_safe_file_list "$file_count")

        # Forbidden patterns that won't match safe files (src/, lib/, tests/, docs/, utils/ with .py/.sh/.js/.ts/.md)
        local forbidden_paths="secrets/,.git/,private/"
        local forbidden_exts=".pem,.key,.p12"

        run validate_file_policy "$files" "$max_files" "0" "$forbidden_paths" "$forbidden_exts"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): expected pass for safe files, got status=$status output='$output'" >&2
            echo "  files: $files" >&2
            echo "  max_files: $max_files, forbidden_paths: $forbidden_paths, forbidden_exts: $forbidden_exts" >&2
            return 1
        }
    done
}

@test "Property 6: Empty file list always passes regardless of policy" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local max_files=$(( (RANDOM % 20) + 1 ))
        local max_lines=$(( (RANDOM % 500) + 1 ))
        local forbidden_paths="*.env,secrets/,.git/"
        local forbidden_exts=".pem,.key,.p12"

        run validate_file_policy "" "$max_files" "$max_lines" "$forbidden_paths" "$forbidden_exts"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): expected pass for empty file list, got status=$status output='$output'" >&2
            return 1
        }
    done
}

@test "Property 6: Glob pattern matching — *.env pattern catches .env files" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Generate a file that matches *.env glob
        local name_len=$(( (RANDOM % 8) + 1 ))
        local name=""
        local chars="abcdefghijklmnopqrstuvwxyz0123456789"
        local j
        for ((j = 0; j < name_len; j++)); do
            name+="${chars:$(( RANDOM % ${#chars} )):1}"
        done
        local env_file="${name}.env"

        # Add some safe files
        local safe_files
        safe_files=$(random_safe_file_list 3)
        local all_files="${safe_files}"$'\n'"${env_file}"

        run validate_file_policy "$all_files" "0" "0" "*.env" ""
        [[ "$status" -ne 0 ]] || {
            echo "FAILED (iteration $i): expected non-zero for file '$env_file' matching '*.env' pattern" >&2
            return 1
        }
        [[ "$output" == *"policy_violation"* ]] || {
            echo "FAILED (iteration $i): expected 'policy_violation' in output, got: '$output'" >&2
            return 1
        }
    done
}

@test "Property 6: Unlimited max_files (0) — file count check is skipped" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Generate a large number of files (10-50)
        local file_count=$(( (RANDOM % 41) + 10 ))
        local files
        files=$(random_safe_file_list "$file_count")

        # max_files=0 means unlimited
        run validate_file_policy "$files" "0" "0" "" ""
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): expected pass with unlimited max_files (0), got status=$status output='$output'" >&2
            return 1
        }
    done
}
