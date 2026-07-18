#!/usr/bin/env bash
# lib/assert.sh — assertion helpers for E2E tests.
# The functions do NOT terminate the script: they accumulate failures in E2E_FAILURES (integer).
# The caller decides the exit code based on the value of E2E_FAILURES.

# Global failure counter (initialized to 0 by the runner).
E2E_FAILURES="${E2E_FAILURES:-0}"

# _assert_pass <msg>
#   Prints [PASS] and the message.
_assert_pass() {
    echo "[PASS] $1"
}

# _assert_fail <msg>
#   Prints [FAIL] and the message; increments E2E_FAILURES.
_assert_fail() {
    echo "[FAIL] $1"
    E2E_FAILURES=$((E2E_FAILURES + 1))
}

# assert_dir_exists <path> <msg>
#   Verifies that the directory exists.
assert_dir_exists() {
    local path="$1"
    local msg="${2:-directory exists: ${path}}"
    if [[ -d "$path" ]]; then
        _assert_pass "$msg"
    else
        _assert_fail "$msg — directory not found: ${path}"
    fi
}

# assert_file_exists <path>
#   Verifies that the file exists.
assert_file_exists() {
    local path="$1"
    if [[ -f "$path" ]]; then
        _assert_pass "file exists: ${path}"
    else
        _assert_fail "file not found: ${path}"
    fi
}

# assert_file_contains <path> <substring>
#   Verifies that the file contains the given substring.
assert_file_contains() {
    local path="$1"
    local substring="$2"
    if [[ ! -f "$path" ]]; then
        _assert_fail "assert_file_contains: file not found: ${path}"
        return
    fi
    if grep -qF "$substring" "$path" 2>/dev/null; then
        _assert_pass "file '${path}' contains '${substring}'"
    else
        _assert_fail "file '${path}' does not contain '${substring}'"
    fi
}

# assert_git_log_nonempty <kb_dir>
#   Verifies that the KB has at least one git commit (written operations).
assert_git_log_nonempty() {
    local kb_dir="$1"
    if [[ ! -d "${kb_dir}/.git" ]]; then
        _assert_fail "assert_git_log_nonempty: not a git repo: ${kb_dir}"
        return
    fi
    local count
    count=$(git -C "$kb_dir" rev-list --count HEAD 2>/dev/null || echo 0)
    if [[ "$count" -gt 0 ]]; then
        _assert_pass "git log not empty in KB '${kb_dir}' (${count} commits)"
    else
        _assert_fail "git log empty in KB '${kb_dir}'"
    fi
}

# assert_concept_exists <kb_dir> <pattern>
#   Looks for a .md file under <kb_dir> that:
#     1. Matches the pattern (passed to find -name)
#     2. Has valid YAML frontmatter (starts with '---')
assert_concept_exists() {
    local kb_dir="$1"
    local pattern="$2"
    local found
    found=$(find "$kb_dir" -name "$pattern" -name "*.md" 2>/dev/null | head -1)

    if [[ -z "$found" ]]; then
        _assert_fail "concept not found (pattern '${pattern}') in ${kb_dir}"
        return
    fi

    # Check YAML frontmatter: the first non-empty line must be '---'
    local first_line
    first_line=$(grep -m1 "." "$found" 2>/dev/null || true)
    if [[ "$first_line" == "---" ]]; then
        _assert_pass "concept found with YAML frontmatter: ${found}"
    else
        _assert_fail "concept found but without valid YAML frontmatter: ${found}"
    fi
}
