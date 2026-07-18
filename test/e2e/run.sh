#!/usr/bin/env bash
# test/e2e/run.sh — E2E agent-level orchestrator for Cartographer.
#
# Usage:
#   ./test/e2e/run.sh [--only <scenario>] [--keep] [--model <model>]
#
#   --only <scenario>  Runs only the given scenario (e.g. 01_mcp_crud).
#   --keep             Does not remove the temporary directories after the run.
#   --model <model>    LLM model to use (overrides E2E_MODEL).
#
# Environment variables:
#   E2E_MODEL     OpenCode model (default: opencode-go/deepseek-v4-flash).
#   E2E_HTTP_PORT Server HTTP port (default: 47821, high port to avoid collisions).
#
# Each scenario is self-contained: it creates its own KB, starts and stops its
# own server, runs the assertions. This orchestrator does not manage servers.

set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${E2E_DIR}/../.." && pwd)"

# AGENT scenarios require an OpenAI-compatible LLM endpoint; OPERATOR ones run
# without it. The actual failure happens in sandbox.sh, here just the warning.
if [[ -z "${E2E_LLM_BASE_URL:-}" ]]; then
    echo "[WARN] E2E_LLM_BASE_URL not set: AGENT scenarios will fail." >&2
    echo "       E.g.: E2E_LLM_BASE_URL=https://api.example.com/v1 make e2e" >&2
fi
export E2E_LLM_BASE_URL="${E2E_LLM_BASE_URL:-}"
export E2E_MODEL="${E2E_MODEL:-opencode-go/deepseek-v4-flash}"
export E2E_HTTP_PORT="${E2E_HTTP_PORT:-47821}"
export REPO_ROOT

ONLY_SCENARIO=""
KEEP=false

# Parse flags
while [[ $# -gt 0 ]]; do
    case "$1" in
        --only)
            ONLY_SCENARIO="$2"
            shift 2
            ;;
        --keep)
            KEEP=true
            shift
            ;;
        --model)
            E2E_MODEL="$2"
            shift 2
            ;;
        *)
            echo "[ERROR] unknown flag: $1" >&2
            exit 1
            ;;
    esac
done

# ---------------------------------------------------------------------------
# Run temp directory (isolated per run)
# ---------------------------------------------------------------------------

export E2E_TMP_DIR
E2E_TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/cartographer_e2e_XXXXXX")"
echo "[run] temp directory: ${E2E_TMP_DIR}"

# ---------------------------------------------------------------------------
# Cleanup (runs in trap; scenarios stop their own servers)
# ---------------------------------------------------------------------------

cleanup() {
    if [[ "$KEEP" == false ]]; then
        rm -rf "$E2E_TMP_DIR"
        echo "[run] cleanup done"
    else
        echo "[run] --keep: temp directory kept: ${E2E_TMP_DIR}"
    fi
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

echo "[run] build (make build)..."
make -C "$REPO_ROOT" build

# ---------------------------------------------------------------------------
# Scenario execution
# ---------------------------------------------------------------------------

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

    # Each scenario is self-contained: it creates its own KB and manages the server.
    if bash "$script"; then
        PASSED=$((PASSED + 1))
    else
        FAILED=$((FAILED + 1))
    fi
}

if [[ -n "$ONLY_SCENARIO" ]]; then
    # Run only the requested scenario
    SCRIPT="${SCENARIOS_DIR}/${ONLY_SCENARIO}.sh"
    if [[ ! -f "$SCRIPT" ]]; then
        echo "[ERROR] scenario not found: ${SCRIPT}" >&2
        exit 1
    fi
    run_scenario "$SCRIPT"
else
    # Run all scenarios in lexicographic order
    for script in "${SCENARIOS_DIR}"/*.sh; do
        [[ -f "$script" ]] || continue
        run_scenario "$script"
    done
fi

# ---------------------------------------------------------------------------
# Final report
# ---------------------------------------------------------------------------

echo ""
echo "======================================================"
echo " E2E Report"
echo "======================================================"
echo " Total   : ${TOTAL}"
echo " Passed  : ${PASSED}"
echo " Failed  : ${FAILED}"
echo " Model   : ${E2E_MODEL}"
echo "======================================================"

if [[ "$FAILED" -gt 0 ]]; then
    echo "[run] FAIL — ${FAILED} scenario(s) failed"
    exit 1
else
    echo "[run] PASS — all scenarios passed"
    exit 0
fi
