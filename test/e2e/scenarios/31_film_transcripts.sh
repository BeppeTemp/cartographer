#!/usr/bin/env bash
# scenarios/31_film_transcripts.sh — the terminal text of the films is the real binary's.
#
# Each film that shows command lines has a script in film/transcripts/<film>.sh that runs the real
# commands in a sandbox and writes film/transcripts/<film>.txt (the page imports it as text).
# This scenario runs every such script into its own temp dir and diffs the result against the
# committed file: a change of CLI output (the plugin preview, an error message, a state name)
# fails here, with the diff and the command that regenerates the file, instead of leaving a
# film that shows output the binary no longer prints.
#
# Expected environment variables: E2E_TMP_DIR, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# shellcheck source=../lib/assert.sh
source "${E2E_DIR}/lib/assert.sh"

SCENARIO_NAME="31_film_transcripts"

echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
if [[ ! -x "$BIN" ]]; then
    echo "[ERROR] binary not found: ${BIN}" >&2
    exit 1
fi

WORK="${E2E_TMP_DIR}/${SCENARIO_NAME}"
mkdir -p "$WORK"

COUNT=0
for script in "${REPO_ROOT}"/film/transcripts/*.sh; do
    [[ -f "$script" ]] || continue
    COUNT=$((COUNT + 1))
    name="$(basename "$script" .sh)"
    committed="${REPO_ROOT}/film/transcripts/${name}.txt"
    fresh="${WORK}/${name}.txt"

    echo "[26:${name}] regenerate and compare..."
    if ! CARTOGRAPHER_BIN="$BIN" bash "$script" "$fresh"; then
        _assert_fail "film/transcripts/${name}.sh failed"
        continue
    fi
    assert_file_exists "$committed"
    if diff -u "$committed" "$fresh" >"${WORK}/${name}.diff"; then
        _assert_pass "film/transcripts/${name}.txt is what the binary prints"
    else
        cat "${WORK}/${name}.diff"
        _assert_fail "film/transcripts/${name}.txt differs from the binary's output; regenerate it with: make build film-transcripts (and look at the film)"
    fi
done

if [[ $COUNT -eq 0 ]]; then
    _assert_fail "no film/transcripts/*.sh found"
fi

echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
