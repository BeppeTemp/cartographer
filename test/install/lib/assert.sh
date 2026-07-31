#!/bin/sh
# lib/assert.sh — POSIX assertion helpers for the test/install/ suite.
# Functions do NOT terminate the script: they accumulate failures in
# INSTALL_FAILURES (integer). The caller decides the exit code.

INSTALL_FAILURES="${INSTALL_FAILURES:-0}"

# _assert_pass <msg>
_assert_pass() {
    printf '[PASS] %s\n' "$1"
}

# _assert_fail <msg>
_assert_fail() {
    printf '[FAIL] %s\n' "$1"
    INSTALL_FAILURES=$((INSTALL_FAILURES + 1))
}

# assert_eq <actual> <expected> <msg>
assert_eq() {
    if [ "$1" = "$2" ]; then
        _assert_pass "$3"
    else
        _assert_fail "$3 — expected '$2', got '$1'"
    fi
}

# assert_contains <haystack> <needle> <msg>
assert_contains() {
    case "$1" in
        *"$2"*) _assert_pass "$3" ;;
        *) _assert_fail "$3 — expected to find '$2' in: $1" ;;
    esac
}

# assert_not_contains <haystack> <needle> <msg>
assert_not_contains() {
    case "$1" in
        *"$2"*) _assert_fail "$3 — unexpectedly found '$2' in: $1" ;;
        *) _assert_pass "$3" ;;
    esac
}

# assert_file_contains <path> <substring> <msg>
assert_file_contains() {
    if [ ! -f "$1" ]; then
        _assert_fail "$3 — file not found: $1"
        return
    fi
    if grep -qF "$2" "$1" 2>/dev/null; then
        _assert_pass "$3"
    else
        _assert_fail "$3 — file '$1' does not contain '$2'"
    fi
}

# assert_file_not_contains <path> <substring> <msg>
assert_file_not_contains() {
    if [ ! -f "$1" ]; then
        _assert_pass "$3 (file absent, trivially lacks '$2')"
        return
    fi
    if grep -qF "$2" "$1" 2>/dev/null; then
        _assert_fail "$3 — file '$1' unexpectedly contains '$2'"
    else
        _assert_pass "$3"
    fi
}

# assert_executable <path> <msg>
assert_executable() {
    if [ -x "$1" ]; then
        _assert_pass "$2"
    else
        _assert_fail "$2 — not an executable file: $1"
    fi
}
