#!/usr/bin/env bash
# setup_suite.bash — bats setup_suite file for integration tests
# Sourced automatically by bats-core before running any test file in this directory.

# Integration tests require a temporary environment with:
# - A mock git repository
# - A mock Aider script
# - Temporary directories for workspaces, logs, locks

SUITE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SUITE_DIR/../.." && pwd)"

setup_suite() {
    export BATS_SUITE_TMPDIR="${BATS_TEST_TMPDIR:-/tmp/bats-integration-$$}"
    mkdir -p "$BATS_SUITE_TMPDIR"
}
