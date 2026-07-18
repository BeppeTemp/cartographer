#!/usr/bin/env bash
# scenarios/08_git_multiclone.sh — OPERATOR scenario: git as synchronization.
#
# Two independent cartographer instances, each on its own clone of the same
# KB-remote (shared bare repo). A write via MCP on instance A gets committed
# and pushed; a subsequent operation on instance B triggers a
# fetch+pull-rebase (SyncIn) and sees the concept created by A.
#
# Filesystem oracle: the concept created on A exists in clone B's working tree.
# Does NOT require the agent (only git + curl MCP).
#
# Expected environment variables: E2E_TMP_DIR, E2E_HTTP_PORT, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# shellcheck source=../lib/assert.sh
source "${E2E_DIR}/lib/assert.sh"

SCENARIO_NAME="08_git_multiclone"
echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
if [[ ! -x "$BIN" ]]; then
    echo "[ERROR] binary not found: ${BIN}" >&2
    exit 1
fi

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
mkdir -p "$DIR"
BARE="${DIR}/remote.git"
KBA="${DIR}/kbA"
KBB="${DIR}/kbB"
PORT_A="${E2E_HTTP_PORT}"
PORT_B="$((E2E_HTTP_PORT + 1))"

PID_A=""
PID_B=""
cleanup() {
    [[ -n "$PID_A" ]] && kill "$PID_A" 2>/dev/null
    [[ -n "$PID_B" ]] && kill "$PID_B" 2>/dev/null
}
trap cleanup EXIT

wait_health() {
    local port="$1" elapsed=0
    while [[ $elapsed -lt 20 ]]; do
        if curl -sf "http://127.0.0.1:${port}/health" 2>/dev/null | grep -q '"kbs"'; then
            return 0
        fi
        sleep 1; elapsed=$((elapsed + 1))
    done
    return 1
}

mcp() {
    local port="$1" kb="$2" payload="$3"
    curl -sf -X POST "http://127.0.0.1:${port}/mcp?kb=${kb}" \
        -H 'content-type: application/json' -d "$payload" >/dev/null
}

# --- Setup: bare remote + clone A initialized as KB ---
echo "[08] init bare remote + KB on clone A"
git init --bare "$BARE" >/dev/null 2>&1
mkdir -p "$KBA"

# Start instance A on KBA with --init (creates structure + git init + initial commit).
CARTOGRAPHER_AUTH=false CARTOGRAPHER_GIT_SYNC=true \
    "$BIN" serve --kb "$KBA" --init --http ":${PORT_A}" >"${DIR}/srvA.log" 2>&1 &
PID_A=$!
wait_health "$PORT_A" || { echo "[ERROR] server A not responding"; exit 1; }

# Connect KBA to the bare repo and initial push (operator operation, direct git).
BRANCH="$(git -C "$KBA" branch --show-current)"
git -C "$KBA" remote add origin "$BARE" 2>/dev/null
git -C "$KBA" push -u origin "$BRANCH" >/dev/null 2>&1

# Clone B from the bare repo and start instance B.
echo "[08] clone B from remote + start instance B"
git clone "$BARE" "$KBB" >/dev/null 2>&1
CARTOGRAPHER_AUTH=false CARTOGRAPHER_GIT_SYNC=true \
    "$BIN" serve --kb "$KBB" --init --http ":${PORT_B}" >"${DIR}/srvB.log" 2>&1 &
PID_B=$!
wait_health "$PORT_B" || { echo "[ERROR] server B not responding"; exit 1; }

# --- A writes a concept (autocommit + push to the bare repo) ---
echo "[08] write on instance A"
mcp "$PORT_A" "kbA" '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"shared","title":"Shared"}}}'
mcp "$PORT_A" "kbA" '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"shared/notes","frontmatter":{"type":"Index","title":"Notes"},"body":"# Notes\n"}}}'
mcp "$PORT_A" "kbA" '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_expand","arguments":{"id":"shared/notes"}}}'
mcp "$PORT_A" "kbA" '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"shared/notes/from-a","frontmatter":{"type":"note","title":"From A"},"body":"Concept created on instance A."}}}'

# --- B performs an operation: SyncIn (fetch+pull) must bring in A's concept ---
echo "[08] operation on instance B (triggers SyncIn)"
mcp "$PORT_B" "kbB" '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"local-b","title":"Local B"}}}'

# --- Assertions (filesystem oracle) ---
echo ""
echo "--- Assertions ---"
CONCEPT_IN_B="${KBB}/data/shared/notes/from-a.md"
assert_file_exists "$CONCEPT_IN_B"
assert_file_contains "$CONCEPT_IN_B" "instance A"

# --- Report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
