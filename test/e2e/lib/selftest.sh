#!/usr/bin/env bash
# lib/selftest.sh — self-test of lib/assert.sh, run by run.sh before the scenarios.
#
# Pins the SIGPIPE trap (D214): under `set -o pipefail`, `echo "$body" | grep -q`
# dies of SIGPIPE when the body outlasts the pipe and the match is early, so a
# passing check reported "lacks" and a leak went undetected. The body here is
# 300 KB (a pipe holds 64 KB) with the needle on line 1, so the old helpers fail
# this test every time and the here-string ones never do. No server is needed.
#
# Exit code: 0 when every helper answered right, 1 otherwise.

set -uo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=assert.sh
source "${SELF_DIR}/assert.sh"

NEEDLE='ALREADY-SENT-NEEDLE'
FILLER="$(head -c 300000 /dev/zero | tr '\0' 'x')"
WITH_NEEDLE="${NEEDLE}"$'\n'"${FILLER}"
WITH_ERROR='{"isError":true,"text":"forbidden: x"}'$'\n'"${FILLER}"
WITHOUT_NEEDLE="${FILLER}"

SELF_FAILURES=0

# expect <pass|fail> <label> <assert_fn> <args...>
#   Runs the helper in a subshell (its failure count is thrown away with it) and
#   reads the verdict from the [PASS]/[FAIL] line it printed first. A helper that
#   does not exist (renamed, typo) or printed neither line (exit 127, a crash)
#   is a failure of the self-test whatever was expected, never a "pass".
expect() {
    local want="$1" label="$2" fn="$3" out first got
    shift 2
    if ! declare -F "$fn" >/dev/null; then
        echo "[selftest] FAIL ${label}: helper ${fn} is not defined"
        SELF_FAILURES=$((SELF_FAILURES + 1))
        return
    fi
    out="$("$fn" "${@:2}" 2>&1)" || true
    first="${out%%$'\n'*}"
    case "$first" in
        '[PASS]'*) got=pass ;;
        '[FAIL]'*) got=fail ;;
        *) got=none ;;
    esac
    if [[ "$got" == "$want" ]]; then
        echo "[selftest] ok   ${label} (${want})"
    else
        echo "[selftest] FAIL ${label}: want ${want}, got ${got}"
        SELF_FAILURES=$((SELF_FAILURES + 1))
    fi
}

expect pass 'assert_mcp_contains finds an early needle in a 300 KB body' \
    assert_mcp_contains d "$WITH_NEEDLE" "$NEEDLE"
expect fail 'assert_mcp_not_discloses sees an early needle in a 300 KB body' \
    assert_mcp_not_discloses d "$WITH_NEEDLE" "$NEEDLE"
expect fail 'assert_mcp_ok sees an early isError in a 300 KB body' \
    assert_mcp_ok d "$WITH_ERROR"
expect pass 'assert_mcp_error_text finds an early error in a 300 KB body' \
    assert_mcp_error_text d "$WITH_ERROR" forbidden
expect fail 'assert_mcp_contains lacks a needle that is not there' \
    assert_mcp_contains d "$WITHOUT_NEEDLE" "$NEEDLE"
expect pass 'assert_mcp_not_discloses passes a body without the needle' \
    assert_mcp_not_discloses d "$WITHOUT_NEEDLE" "$NEEDLE"
expect pass 'assert_mcp_ok passes a body without isError' \
    assert_mcp_ok d "$WITHOUT_NEEDLE"

# The body must really reproduce the trap, or the checks above prove nothing.
# The body is larger than a pipe, so the old form dies of SIGPIPE every time
# (the status is 141 under pipefail): if it does not, the control no longer
# proves the trap and the self-test fails rather than stay green.
if echo "$WITH_NEEDLE" | grep -q "$NEEDLE"; then # e2e-pipe-ok: the trap itself
    echo "[selftest] FAIL control: the old pipe form did not hit SIGPIPE: the 300 KB body no longer proves the trap, so the cases above prove nothing"
    SELF_FAILURES=$((SELF_FAILURES + 1))
else
    echo "[selftest] ok   control: the old pipe form hits SIGPIPE on this host"
fi

if [[ "$SELF_FAILURES" -gt 0 ]]; then
    echo "[selftest] FAIL — ${SELF_FAILURES} helper(s) answered wrong"
    exit 1
fi
echo "[selftest] PASS"
