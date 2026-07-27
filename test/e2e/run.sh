#!/usr/bin/env bash
# Deterministic end-to-end test orchestrator.
#
# Usage:
#   ./test/e2e/run.sh [--only <scenario>] [--keep]
#
# Every scenario drives the real binary through HTTP/CLI and asserts on
# filesystem, git state, HTTP status or process exit codes. No LLM is involved.

set -euo pipefail

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${E2E_DIR}/../.." && pwd)"

export E2E_HTTP_PORT="${E2E_HTTP_PORT:-47821}"
export REPO_ROOT

ONLY_SCENARIO=""
KEEP=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --only)
            [[ $# -ge 2 ]] || { echo "[ERROR] --only requires a scenario" >&2; exit 2; }
            ONLY_SCENARIO="$2"
            shift 2
            ;;
        --keep)
            KEEP=true
            shift
            ;;
        *)
            echo "[ERROR] unknown flag: $1" >&2
            exit 2
            ;;
    esac
done

export E2E_TMP_DIR
E2E_TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/cartographer_e2e_XXXXXX")"
echo "[run] temp directory: ${E2E_TMP_DIR}"

cleanup() {
    if [[ "$KEEP" == false ]]; then
        rm -rf "$E2E_TMP_DIR"
        echo "[run] cleanup done"
    else
        echo "[run] --keep: temp directory kept: ${E2E_TMP_DIR}"
    fi
}
trap cleanup EXIT

echo "[run] build..."
make -C "$REPO_ROOT" build

SCENARIOS_DIR="${E2E_DIR}/scenarios"
TOTAL=0
FAILED=0
PASSED=0

run_scenario() {
    local script="$1"
    local name
    name="$(basename "$script" .sh)"
    TOTAL=$((TOTAL + 1))
    echo ""
    echo "======================================================"
    echo " Scenario: ${name}"
    echo "======================================================"

    if bash "$script"; then
        PASSED=$((PASSED + 1))
    else
        FAILED=$((FAILED + 1))
    fi
}

if [[ -n "$ONLY_SCENARIO" ]]; then
    script="${SCENARIOS_DIR}/${ONLY_SCENARIO}.sh"
    [[ -f "$script" ]] || { echo "[ERROR] scenario not found: ${script}" >&2; exit 2; }
    run_scenario "$script"
else
    for script in "${SCENARIOS_DIR}"/*.sh; do
        [[ -f "$script" ]] || continue
        run_scenario "$script"
    done
fi

echo ""
echo "======================================================"
echo " E2E Report"
echo "======================================================"
echo " Total   : ${TOTAL}"
echo " Passed  : ${PASSED}"
echo " Failed  : ${FAILED}"
echo "======================================================"

if [[ "$FAILED" -gt 0 ]]; then
    echo "[run] FAIL — ${FAILED} scenario(s) failed"
    exit 1
fi

echo "[run] PASS — all scenarios passed"
