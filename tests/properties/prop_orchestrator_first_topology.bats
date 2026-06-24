#!/usr/bin/env bats

setup() {
    PROJECT_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
    GROUP_VARS_FILE="${PROJECT_ROOT}/group_vars/all.yml"
    BOOTSTRAP_FILE="${PROJECT_ROOT}/playbooks/bootstrap.yml"
}

@test "orchestrator-first defaults enable orchestrator and aider" {
    run grep -Eq "^orchestrator_enabled: .*default\\('true'" "$GROUP_VARS_FILE"
    [ "$status" -eq 0 ]

    run grep -Eq "^aider_enabled: .*default\\('true'" "$GROUP_VARS_FILE"
    [ "$status" -eq 0 ]
}

@test "llama.cpp exposes orchestrator aliases" {
    run grep -Eq 'alias: coder$' "$GROUP_VARS_FILE"
    [ "$status" -eq 0 ]

    run grep -Eq 'alias: planner$' "$GROUP_VARS_FILE"
    [ "$status" -eq 0 ]

    run grep -Eq 'alias: utility$' "$GROUP_VARS_FILE"
    [ "$status" -eq 0 ]
}

@test "bootstrap gates retired stack cleanup behind an explicit flag" {
    run grep -Eq "^retired_stack_cleanup_enabled: .*default\\('false'" "$GROUP_VARS_FILE"
    [ "$status" -eq 0 ]

    run grep -Eq 'when: retired_stack_cleanup_enabled' "$BOOTSTRAP_FILE"
    [ "$status" -eq 0 ]
}
