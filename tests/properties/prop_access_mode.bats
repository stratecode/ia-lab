#!/usr/bin/env bats
# Feature: agent-runtime, Property 12: Access Mode Enforcement
# Validates: Requirements 24.2, 24.3, 24.4, 24.5

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
}

@test "Property 12: full-edit mode allows write operations" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        run check_access_mode "full-edit" "write"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): full-edit + write should be allowed, got status=$status output='$output'" >&2
            return 1
        }
        [[ -z "$output" ]] || {
            echo "FAILED (iteration $i): full-edit + write should produce no output, got '$output'" >&2
            return 1
        }
    done
}

@test "Property 12: full-edit mode allows read operations" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        run check_access_mode "full-edit" "read"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): full-edit + read should be allowed, got status=$status output='$output'" >&2
            return 1
        }
    done
}

@test "Property 12: review-only mode rejects write operations with permission_violation" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        run check_access_mode "review-only" "write"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): review-only + write should be rejected, got status=$status" >&2
            return 1
        }
        [[ "$output" == *"permission_violation"* ]] || {
            echo "FAILED (iteration $i): expected 'permission_violation' in output, got '$output'" >&2
            return 1
        }
        [[ "$output" == *"review-only"* ]] || {
            echo "FAILED (iteration $i): expected mode 'review-only' in output, got '$output'" >&2
            return 1
        }
    done
}

@test "Property 12: review-only mode allows read operations" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        run check_access_mode "review-only" "read"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): review-only + read should be allowed, got status=$status output='$output'" >&2
            return 1
        }
    done
}

@test "Property 12: readonly mode rejects write operations with permission_violation" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        run check_access_mode "readonly" "write"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): readonly + write should be rejected, got status=$status" >&2
            return 1
        }
        [[ "$output" == *"permission_violation"* ]] || {
            echo "FAILED (iteration $i): expected 'permission_violation' in output, got '$output'" >&2
            return 1
        }
        [[ "$output" == *"readonly"* ]] || {
            echo "FAILED (iteration $i): expected mode 'readonly' in output, got '$output'" >&2
            return 1
        }
    done
}

@test "Property 12: readonly mode allows read operations" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        run check_access_mode "readonly" "read"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): readonly + read should be allowed, got status=$status output='$output'" >&2
            return 1
        }
    done
}

@test "Property 12: Random mode and operation combinations — write allowed only for full-edit" {
    local access_modes=("full-edit" "review-only" "readonly")
    local operations=("write" "read")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local mode operation
        mode=$(random_choice "${access_modes[@]}")
        operation=$(random_choice "${operations[@]}")

        run check_access_mode "$mode" "$operation"

        if [[ "$operation" == "read" ]]; then
            # Read operations are always allowed regardless of mode
            [[ "$status" -eq 0 ]] || {
                echo "FAILED (iteration $i): read operation should always be allowed (mode='$mode'), got status=$status output='$output'" >&2
                return 1
            }
        elif [[ "$operation" == "write" ]]; then
            if [[ "$mode" == "full-edit" ]]; then
                # Write allowed only for full-edit
                [[ "$status" -eq 0 ]] || {
                    echo "FAILED (iteration $i): write should be allowed for full-edit, got status=$status output='$output'" >&2
                    return 1
                }
            else
                # Write rejected for review-only and readonly
                [[ "$status" -eq 1 ]] || {
                    echo "FAILED (iteration $i): write should be rejected for mode='$mode', got status=$status" >&2
                    return 1
                }
                [[ "$output" == *"permission_violation"* ]] || {
                    echo "FAILED (iteration $i): expected 'permission_violation' for mode='$mode' + write, got '$output'" >&2
                    return 1
                }
            fi
        fi
    done
}

@test "Property 12: Default access mode is full-edit when not specified" {
    # When access_mode is empty, check_access_mode defaults to full-edit
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        run check_access_mode "" "write"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): empty mode should default to full-edit (allow write), got status=$status output='$output'" >&2
            return 1
        }
    done
}
