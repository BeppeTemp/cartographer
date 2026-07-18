#!/usr/bin/env bash
# scenarios/09_git_conflict.sh — OPERATOR scenario: agentic conflict handling (Step 3).
#
# Two cartographer instances on two clones of the same KB-remote (bare). A git
# rebase conflict is deliberately provoked; we verify that Cartographer:
#   1. Detects the conflict during SyncIn,
#   2. Writes the registry to .cartographer/conflicts.json,
#   3. Marks the involved concept as status: degraded in the frontmatter.
#
# Technique for provoking the conflict:
#   - A creates shared/notes/c and pushes (C_body1).
#   - B syncs (SyncIn) and rewrites the same concept with a different body (C_body2, push).
#   - Directly on clone A (without the MCP server) a third body is committed (C_body3,
#     not pushed) — local divergence.
#   - The next MCP operation on A triggers SyncIn: fetch sees C_body2, pull-rebase
#     tries to reapply C_body3 on top of C_body2 → CONFLICT → registry + degraded.
#
# Filesystem oracle:
#   - <kbA>/.cartographer/conflicts.json exists and contains "shared/notes/c".
#   - <kbA>/data/shared/notes/c.md contains "status: degraded".
#
# Expected environment variables: E2E_TMP_DIR, E2E_HTTP_PORT, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# shellcheck source=../lib/assert.sh
source "${E2E_DIR}/lib/assert.sh"

SCENARIO_NAME="09_git_conflict"
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
    [[ -n "$PID_A" ]] && kill "$PID_A" 2>/dev/null || true
    [[ -n "$PID_B" ]] && kill "$PID_B" 2>/dev/null || true
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

mcp_capture() {
    local port="$1" kb="$2" payload="$3"
    curl -sf -X POST "http://127.0.0.1:${port}/mcp?kb=${kb}" \
        -H 'content-type: application/json' -d "$payload"
}

# --- Setup: bare remote + clone A initialized as KB ---
echo "[09] init bare remote + KB on clone A"
git init --bare "$BARE" >/dev/null 2>&1
mkdir -p "$KBA"

CARTOGRAPHER_AUTH=false CARTOGRAPHER_GIT_SYNC=true \
    "$BIN" serve --kb "$KBA" --init --http ":${PORT_A}" >"${DIR}/srvA.log" 2>&1 &
PID_A=$!
wait_health "$PORT_A" || { echo "[ERROR] server A not responding"; cat "${DIR}/srvA.log" >&2; exit 1; }

# Connect KBA to the bare repo and initial push.
BRANCH="$(git -C "$KBA" branch --show-current)"
git -C "$KBA" remote add origin "$BARE" >/dev/null 2>&1
git -C "$KBA" push -u origin "$BRANCH" >/dev/null 2>&1

# Clone B from the bare repo and start instance B.
echo "[09] clone B from remote + start instance B"
git clone "$BARE" "$KBB" >/dev/null 2>&1
git -C "$KBB" config user.email "test@wiki.local" && git -C "$KBB" config user.name "Wiki Test"

CARTOGRAPHER_AUTH=false CARTOGRAPHER_GIT_SYNC=true \
    "$BIN" serve --kb "$KBB" --init --http ":${PORT_B}" >"${DIR}/srvB.log" 2>&1 &
PID_B=$!
wait_health "$PORT_B" || { echo "[ERROR] server B not responding"; cat "${DIR}/srvB.log" >&2; exit 1; }

# --- A writes shared/notes/c with body1 (commit + push) ---
echo "[09] A: creates shared/notes/c with body1"
mcp "$PORT_A" "kbA" '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"shared","title":"Shared"}}}'
mcp "$PORT_A" "kbA" '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"shared/notes","frontmatter":{"type":"Index","title":"Notes"},"body":"# Notes\n"}}}'
mcp "$PORT_A" "kbA" '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_expand","arguments":{"id":"shared/notes"}}}'
mcp "$PORT_A" "kbA" '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"shared/notes/c","frontmatter":{"type":"note","title":"Shared Note C"},"body":"body1: written by A.\n"}}}'

# --- B syncs (SyncIn pulls in A's commits) and rewrites the same concept with body2 ---
echo "[09] B: syncs + rewrites shared/notes/c with body2"
mcp "$PORT_B" "kbB" '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"local-b","title":"Local B"}}}'
mcp "$PORT_B" "kbB" '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"shared/notes/c","frontmatter":{"type":"note","title":"Shared Note C"},"body":"body2: rewritten by B.\n"}}}'

# --- Create local divergence on A: direct commit (C_body3) bypassing the server ---
echo "[09] A: direct commit of body3 (local divergence, no push)"
CONCEPT_A="${KBA}/data/shared/notes/c.md"
printf -- '---\ntype: note\ntitle: Shared Note C\n---\nbody3: rewritten directly by A (no MCP).\n' > "$CONCEPT_A"
git -C "$KBA" add -A >/dev/null 2>&1
git -C "$KBA" commit -m "A: body3 direct commit" >/dev/null 2>&1

# --- A performs any MCP operation: SyncIn must detect the conflict ---
echo "[09] A: MCP operation that triggers SyncIn (must detect the conflict)"
# log_append writes to data/log.md → gitWrap → SyncIn → fetch C_body2 → rebase C_body3 → CONFLICT
mcp "$PORT_A" "kbA" '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"log_append","arguments":{"entry":"conflict test entry"}}}' || true

# Brief wait to make sure the server has written to disk.
sleep 1

# --- Assertions (filesystem oracle) ---
echo ""
echo "--- Assertions ---"

CONFLICTS_JSON="${KBA}/.cartographer/conflicts.json"
assert_file_exists "$CONFLICTS_JSON"
assert_file_contains "$CONFLICTS_JSON" "shared/notes/c"

assert_file_exists "$CONCEPT_A"
assert_file_contains "$CONCEPT_A" "status: degraded"

# --- Optional: verify via conflicts_list that the conflict is listed ---
echo "[09] verifying conflicts_list via MCP"
CONFLICTS_OUT="$(mcp_capture "$PORT_A" "kbA" '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"conflicts_list","arguments":{}}}')"
if echo "$CONFLICTS_OUT" | grep -q "shared/notes/c"; then
    _assert_pass "conflicts_list lists shared/notes/c"
else
    _assert_fail "conflicts_list does not list shared/notes/c (output: ${CONFLICTS_OUT})"
fi

# --- Report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    echo "--- Server A log ---"
    tail -30 "${DIR}/srvA.log" >&2
    exit 1
fi
