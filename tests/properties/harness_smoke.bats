#!/usr/bin/env bats
# harness_smoke.bats — Smoke test to verify the property test harness works correctly.
# Feature: agent-runtime, Property N/A: Harness validation

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
    # Create mock repos dir for validate_params tests
    mkdir -p "${AIDER_REPOS_DIR}/test-repo"
}

@test "PROPERTY_ITERATIONS is set to 100" {
    [[ "$PROPERTY_ITERATIONS" -eq 100 ]]
}

@test "random_string generates non-empty strings" {
    local result
    result=$(random_string 32)
    [[ -n "$result" ]]
}

@test "random_string respects max_len" {
    for i in $(seq 1 20); do
        local result
        result=$(random_string 10)
        local len=${#result}
        [[ $len -ge 1 && $len -le 10 ]]
    done
}

@test "random_uuid generates valid UUID v4 format" {
    local pattern='^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    for i in $(seq 1 20); do
        local uuid
        uuid=$(random_uuid)
        [[ "$uuid" =~ $pattern ]]
    done
}

@test "random_invalid_uuid generates strings that fail UUID v4 validation" {
    for i in $(seq 1 20); do
        local invalid
        invalid=$(random_invalid_uuid)
        # Should NOT pass validate_uuid
        run validate_uuid "$invalid"
        [[ "$status" -ne 0 ]]
    done
}

@test "random_file_path generates paths with slashes" {
    for i in $(seq 1 20); do
        local path
        path=$(random_file_path)
        [[ "$path" == */* ]]
    done
}

@test "random_number generates values within range" {
    for i in $(seq 1 20); do
        local num
        num=$(random_number 5 50)
        [[ $num -ge 5 && $num -le 50 ]]
    done
}

@test "random_choice picks from provided options" {
    local options=("alpha" "beta" "gamma" "delta")
    for i in $(seq 1 20); do
        local choice
        choice=$(random_choice "${options[@]}")
        [[ "$choice" == "alpha" || "$choice" == "beta" || "$choice" == "gamma" || "$choice" == "delta" ]]
    done
}

@test "random_json_string generates non-empty output" {
    for i in $(seq 1 10); do
        local json
        json=$(random_json_string)
        [[ -n "$json" ]]
    done
}

@test "assert_matches_regex passes for matching values" {
    assert_matches_regex "hello123" '^[a-z0-9]+$'
}

@test "assert_matches_regex fails for non-matching values" {
    run assert_matches_regex "HELLO" '^[a-z]+$'
    [[ "$status" -ne 0 ]]
}

@test "assert_not_matches_regex passes for non-matching values" {
    assert_not_matches_regex "HELLO" '^[a-z]+$'
}

@test "assert_not_matches_regex fails for matching values" {
    run assert_not_matches_regex "hello" '^[a-z]+$'
    [[ "$status" -ne 0 ]]
}

@test "assert_exit_code passes for matching codes" {
    assert_exit_code "0" "0"
    assert_exit_code "1" "1"
}

@test "assert_exit_code fails for mismatched codes" {
    run assert_exit_code "0" "1"
    [[ "$status" -ne 0 ]]
}

@test "library functions are available (generate_branch_slug)" {
    local result
    result=$(generate_branch_slug "Hello World")
    [[ -n "$result" ]]
}

@test "library functions are available (validate_uuid)" {
    local uuid
    uuid=$(random_uuid)
    validate_uuid "$uuid"
}

@test "property iteration loop pattern works" {
    local count=0
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local uuid
        uuid=$(random_uuid)
        validate_uuid "$uuid"
        count=$((count + 1))
    done
    [[ $count -eq 100 ]]
}
