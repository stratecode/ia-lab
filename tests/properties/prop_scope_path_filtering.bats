#!/usr/bin/env bats
# Feature: agent-runtime, Property 14: Scope Path Filtering
# Validates: Requirements 31.1, 31.4

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
}

# ─── Generators ──────────────────────────────────────────────────────────────

# random_scope_path()
# Generates a random directory prefix (e.g., "src/", "lib/utils/")
random_scope_path() {
    local dir_names=("src" "lib" "tests" "docs" "config" "utils" "helpers" "modules" "pkg" "internal" "app" "api" "core" "services")
    local depth=$(( (RANDOM % 3) + 1 ))
    local path=""
    local i

    for ((i = 0; i < depth; i++)); do
        local dir_idx=$(( RANDOM % ${#dir_names[@]} ))
        path+="${dir_names[$dir_idx]}/"
    done

    printf '%s' "$path"
}

# random_file_in_scope(scope_path)
# Generates a random file path that IS within the given scope path.
random_file_in_scope() {
    local scope="$1"
    local name_len=$(( (RANDOM % 10) + 3 ))
    local name=""
    local chars="abcdefghijklmnopqrstuvwxyz0123456789_-"
    local extensions=(".py" ".sh" ".js" ".ts" ".yml" ".json" ".md" ".txt")
    local i

    for ((i = 0; i < name_len; i++)); do
        name+="${chars:$(( RANDOM % ${#chars} )):1}"
    done

    local ext_idx=$(( RANDOM % ${#extensions[@]} ))

    # Optionally add subdirectory depth within scope
    local sub=""
    if (( RANDOM % 3 == 0 )); then
        local sub_dirs=("sub" "nested" "deep" "inner")
        local sub_idx=$(( RANDOM % ${#sub_dirs[@]} ))
        sub="${sub_dirs[$sub_idx]}/"
    fi

    printf '%s%s%s%s' "$scope" "$sub" "$name" "${extensions[$ext_idx]}"
}

# random_file_outside_scope(scope_paths_array)
# Generates a random file path that is NOT within any of the given scope paths.
random_file_outside_scope() {
    local -a scopes=("$@")
    local outside_dirs=("other" "vendor" "dist" "build" "tmp" "external" "third_party" "generated")
    local dir_idx=$(( RANDOM % ${#outside_dirs[@]} ))
    local base="${outside_dirs[$dir_idx]}/"

    # Ensure it doesn't accidentally match any scope
    local matched=1
    while (( matched )); do
        matched=0
        for scope in "${scopes[@]}"; do
            if [[ "$base" == "$scope"* ]]; then
                matched=1
                dir_idx=$(( (dir_idx + 1) % ${#outside_dirs[@]} ))
                base="${outside_dirs[$dir_idx]}_extra/"
                break
            fi
        done
    done

    local name_len=$(( (RANDOM % 8) + 3 ))
    local name=""
    local chars="abcdefghijklmnopqrstuvwxyz0123456789"
    local extensions=(".py" ".sh" ".js" ".ts" ".yml")
    local i

    for ((i = 0; i < name_len; i++)); do
        name+="${chars:$(( RANDOM % ${#chars} )):1}"
    done

    local ext_idx=$(( RANDOM % ${#extensions[@]} ))
    printf '%s%s%s' "$base" "$name" "${extensions[$ext_idx]}"
}

# ─── Property Tests ──────────────────────────────────────────────────────────

@test "Property 14: filtered set contains only files within scope paths" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Generate 1-3 random scope paths
        local num_scopes=$(( (RANDOM % 3) + 1 ))
        local -a scope_array=()
        local scope_csv=""
        local s

        for ((s = 0; s < num_scopes; s++)); do
            local sp
            sp=$(random_scope_path)
            scope_array+=("$sp")
            if [[ -n "$scope_csv" ]]; then
                scope_csv+=","
            fi
            scope_csv+="$sp"
        done

        # Generate a mix of files: some in scope, some outside
        local num_in=$(( (RANDOM % 5) + 1 ))
        local num_out=$(( (RANDOM % 5) + 1 ))
        local file_list=""
        local j

        for ((j = 0; j < num_in; j++)); do
            local scope_idx=$(( RANDOM % num_scopes ))
            local f
            f=$(random_file_in_scope "${scope_array[$scope_idx]}")
            if [[ -n "$file_list" ]]; then
                file_list+=$'\n'
            fi
            file_list+="$f"
        done

        for ((j = 0; j < num_out; j++)); do
            local f
            f=$(random_file_outside_scope "${scope_array[@]}")
            if [[ -n "$file_list" ]]; then
                file_list+=$'\n'
            fi
            file_list+="$f"
        done

        # Run filter
        local result
        result=$(filter_scope_paths "$file_list" "$scope_csv")

        # Assert: every file in the result is within at least one scope path
        if [[ -n "$result" ]]; then
            while IFS= read -r file; do
                [[ -z "$file" ]] && continue
                local in_scope=0
                for scope in "${scope_array[@]}"; do
                    if [[ "$file" == "$scope"* ]]; then
                        in_scope=1
                        break
                    fi
                done
                (( in_scope == 1 )) || {
                    echo "FAILED: file '$file' is NOT within any scope path (scopes: ${scope_csv}), iteration: $i" >&2
                    return 1
                }
            done <<< "$result"
        fi
    done
}

@test "Property 14: no file outside scope is included in filtered set" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Generate 1-2 scope paths
        local num_scopes=$(( (RANDOM % 2) + 1 ))
        local -a scope_array=()
        local scope_csv=""
        local s

        for ((s = 0; s < num_scopes; s++)); do
            local sp
            sp=$(random_scope_path)
            scope_array+=("$sp")
            if [[ -n "$scope_csv" ]]; then
                scope_csv+=","
            fi
            scope_csv+="$sp"
        done

        # Generate only files OUTSIDE scope
        local num_files=$(( (RANDOM % 5) + 1 ))
        local file_list=""
        local j

        for ((j = 0; j < num_files; j++)); do
            local f
            f=$(random_file_outside_scope "${scope_array[@]}")
            if [[ -n "$file_list" ]]; then
                file_list+=$'\n'
            fi
            file_list+="$f"
        done

        # Run filter
        local result
        result=$(filter_scope_paths "$file_list" "$scope_csv")

        # Assert: result should be empty (no files match scope)
        [[ -z "$result" ]] || {
            echo "FAILED: expected empty result but got '$result' (scopes: ${scope_csv}, files: ${file_list}), iteration: $i" >&2
            return 1
        }
    done
}

@test "Property 14: all in-scope files are preserved in filtered set" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Generate a single scope path
        local scope
        scope=$(random_scope_path)

        # Generate only files INSIDE scope
        local num_files=$(( (RANDOM % 5) + 1 ))
        local file_list=""
        local -a expected_files=()
        local j

        for ((j = 0; j < num_files; j++)); do
            local f
            f=$(random_file_in_scope "$scope")
            expected_files+=("$f")
            if [[ -n "$file_list" ]]; then
                file_list+=$'\n'
            fi
            file_list+="$f"
        done

        # Run filter
        local result
        result=$(filter_scope_paths "$file_list" "$scope")

        # Assert: every expected file is in the result
        for f in "${expected_files[@]}"; do
            local found=0
            while IFS= read -r line; do
                if [[ "$line" == "$f" ]]; then
                    found=1
                    break
                fi
            done <<< "$result"
            (( found == 1 )) || {
                echo "FAILED: in-scope file '$f' missing from result (scope: $scope), iteration: $i" >&2
                return 1
            }
        done
    done
}

@test "Property 14: empty scope paths returns all files unfiltered" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Generate random file list
        local num_files=$(( (RANDOM % 5) + 1 ))
        local file_list=""
        local j

        for ((j = 0; j < num_files; j++)); do
            local f
            f=$(random_file_path)
            if [[ -n "$file_list" ]]; then
                file_list+=$'\n'
            fi
            file_list+="$f"
        done

        # Run filter with empty scope
        local result
        result=$(filter_scope_paths "$file_list" "")

        # Assert: result equals input (all files returned)
        [[ "$result" == "$file_list" ]] || {
            echo "FAILED: empty scope should return all files, got '$result' vs expected '$file_list', iteration: $i" >&2
            return 1
        }
    done
}
