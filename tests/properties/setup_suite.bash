#!/usr/bin/env bash
# setup_suite.bash — bats setup_suite file for property tests
# Sourced automatically by bats-core before running any test file in this directory.

# Source the test helper which provides generators, assertions, and library access
SUITE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SUITE_DIR}/test_helper.bash"

# Create mock repos directory for tests that need it
setup_suite() {
    export BATS_SUITE_TMPDIR="${BATS_TEST_TMPDIR:-/tmp/bats-suite-$$}"
    mkdir -p "$BATS_SUITE_TMPDIR"
    mkdir -p "${AIDER_REPOS_DIR}"
}
