#!/usr/bin/env bats
# prop_commit_message.bats — Property-based test for format_commit_message()
# Feature: agent-runtime, Property 4: Commit Message Format
# **Validates: Requirements 8.2**

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
}

# ─── Helper: generate random description of specific length ──────────────────
# Generates a string of exactly the given length using safe ASCII characters.
random_description() {
    local len="${1:-50}"
    local chars="abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 -_."
    local result=""
    local i
    for ((i = 0; i < len; i++)); do
        result+="${chars:$(( RANDOM % ${#chars} )):1}"
    done
    printf '%s' "$result"
}

@test "Property 4: format_commit_message produces correct format for random inputs" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local uuid
        uuid=$(random_uuid)

        # Generate description of varying length (1-200 chars)
        local desc_len=$(( (RANDOM % 200) + 1 ))
        local description
        description=$(random_description "$desc_len")

        # Call the function under test
        local result
        result=$(format_commit_message "$uuid" "$description")

        # Assert: output starts with "agent("
        [[ "$result" == agent\(* ]]

        # Assert: format matches agent(<uuid>): <desc>
        local pattern="^agent\($uuid\): .+"
        [[ "$result" =~ $pattern ]]

        # Assert: the UUID in the output matches the input UUID
        local extracted_uuid="${result#agent(}"
        extracted_uuid="${extracted_uuid%%):*}"
        [[ "$extracted_uuid" == "$uuid" ]]

        # Assert: description portion (after ": ") is at most 72 characters
        local desc_portion="${result#*): }"
        [[ ${#desc_portion} -le 72 ]]
    done
}

@test "Property 4: very long descriptions (>72 chars) are truncated" {
    for i in $(seq 1 20); do
        local uuid
        uuid=$(random_uuid)

        # Generate description longer than 72 chars (100-200 chars)
        local desc_len=$(( (RANDOM % 101) + 100 ))
        local description
        description=$(random_description "$desc_len")

        local result
        result=$(format_commit_message "$uuid" "$description")

        # Extract description portion after ": "
        local desc_portion="${result#*): }"

        # Must be exactly 72 characters (truncated)
        [[ ${#desc_portion} -eq 72 ]]

        # Truncated portion must match the first 72 chars of the input
        [[ "$desc_portion" == "${description:0:72}" ]]
    done
}

@test "Property 4: short descriptions (≤72 chars) are preserved as-is" {
    for i in $(seq 1 20); do
        local uuid
        uuid=$(random_uuid)

        # Generate description of 1-72 chars
        local desc_len=$(( (RANDOM % 72) + 1 ))
        local description
        description=$(random_description "$desc_len")

        local result
        result=$(format_commit_message "$uuid" "$description")

        # Extract description portion after ": "
        local desc_portion="${result#*): }"

        # Must be preserved exactly as-is
        [[ "$desc_portion" == "$description" ]]
    done
}
