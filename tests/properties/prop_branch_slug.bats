#!/usr/bin/env bats
# Feature: agent-runtime, Property 1: Branch Name Slug Generation
# Validates: Requirements 4.2

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
}

@test "Property 1: slug contains only lowercase alphanumeric and hyphens, no consecutive hyphens, no leading/trailing hyphens, max 50 chars" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local input
        input=$(random_string 64)

        run generate_branch_slug "$input"
        [[ "$status" -eq 0 ]]

        local slug="$output"

        # Must contain only lowercase alphanumeric characters and hyphens
        [[ "$slug" =~ ^[a-z0-9-]*$ ]] || {
            echo "FAILED: slug '$slug' contains invalid characters (input: '$input', iteration: $i)" >&2
            return 1
        }

        # No consecutive hyphens
        [[ "$slug" != *--* ]] || {
            echo "FAILED: slug '$slug' contains consecutive hyphens (input: '$input', iteration: $i)" >&2
            return 1
        }

        # No leading hyphen
        [[ "$slug" != -* ]] || {
            echo "FAILED: slug '$slug' has leading hyphen (input: '$input', iteration: $i)" >&2
            return 1
        }

        # No trailing hyphen
        [[ "$slug" != *- ]] || {
            echo "FAILED: slug '$slug' has trailing hyphen (input: '$input', iteration: $i)" >&2
            return 1
        }

        # Maximum 50 characters
        [[ ${#slug} -le 50 ]] || {
            echo "FAILED: slug '$slug' exceeds 50 chars (length: ${#slug}, input: '$input', iteration: $i)" >&2
            return 1
        }
    done
}

@test "Property 1: empty or whitespace-only input produces 'task'" {
    # Empty string
    run generate_branch_slug ""
    [[ "$status" -eq 0 ]]
    [[ "$output" == "task" ]] || {
        echo "FAILED: empty input produced '$output' instead of 'task'" >&2
        return 1
    }

    # Whitespace-only inputs
    run generate_branch_slug "   "
    [[ "$status" -eq 0 ]]
    [[ "$output" == "task" ]] || {
        echo "FAILED: spaces-only input produced '$output' instead of 'task'" >&2
        return 1
    }

    run generate_branch_slug "	"
    [[ "$status" -eq 0 ]]
    [[ "$output" == "task" ]] || {
        echo "FAILED: tab-only input produced '$output' instead of 'task'" >&2
        return 1
    }
}

@test "Property 1: unicode-only and special-char-only inputs produce 'task'" {
    # Pure unicode characters (no alphanumeric content)
    local unicode_inputs=("中文日本語" "🚀🎉💻🔥" "ñüéßøå" "→∞½±×÷" "αβγδε" "العربية")

    for input in "${unicode_inputs[@]}"; do
        run generate_branch_slug "$input"
        [[ "$status" -eq 0 ]]
        [[ "$output" == "task" ]] || {
            echo "FAILED: unicode-only input '$input' produced '$output' instead of 'task'" >&2
            return 1
        }
    done

    # Pure special characters (no alphanumeric content)
    local special_inputs=("!@#\$%^&*()" "[]{}|;:,.<>?" "+++///\\\\")

    for input in "${special_inputs[@]}"; do
        run generate_branch_slug "$input"
        [[ "$status" -eq 0 ]]
        [[ "$output" == "task" ]] || {
            echo "FAILED: special-char-only input '$input' produced '$output' instead of 'task'" >&2
            return 1
        }
    done
}

@test "Property 1: very long inputs (>50 chars) are truncated to at most 50 characters" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Generate a long alphanumeric string guaranteed to be >50 chars
        local input=""
        local target_len=$(( (RANDOM % 100) + 51 ))
        local chars="abcdefghijklmnopqrstuvwxyz0123456789"
        local j
        for ((j = 0; j < target_len; j++)); do
            input+="${chars:$(( RANDOM % ${#chars} )):1}"
        done

        run generate_branch_slug "$input"
        [[ "$status" -eq 0 ]]

        local slug="$output"

        # Must be at most 50 characters
        [[ ${#slug} -le 50 ]] || {
            echo "FAILED: slug '$slug' exceeds 50 chars (length: ${#slug}, input length: ${#input}, iteration: $i)" >&2
            return 1
        }

        # Still must satisfy all other properties
        [[ "$slug" =~ ^[a-z0-9-]*$ ]] || {
            echo "FAILED: slug '$slug' contains invalid characters (iteration: $i)" >&2
            return 1
        }
        [[ "$slug" != *--* ]] || {
            echo "FAILED: slug '$slug' contains consecutive hyphens (iteration: $i)" >&2
            return 1
        }
        [[ "$slug" != -* ]] || {
            echo "FAILED: slug '$slug' has leading hyphen (iteration: $i)" >&2
            return 1
        }
        [[ "$slug" != *- ]] || {
            echo "FAILED: slug '$slug' has trailing hyphen (iteration: $i)" >&2
            return 1
        }
    done
}
