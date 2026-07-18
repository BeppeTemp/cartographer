#!/usr/bin/env bash
# scenarios/10_scoped_tokens.sh — OPERATOR scenario: per-KB r/rw scope enforcement (M2, D45).
#
# Verifies (operator channel only, curl — no agent/model):
#   1. No-scope (admin) token → full access: read and write on any KB = 200.
#   2. Token with scope `kb:<kbA>:r` (read-only) → read on kbA = 200, write on kbA = 403.
#   3. Token with scope `kb:<kbA>:rw` → read and write on kbA = 200.
#   4. Cross-KB: token scoped only to kbA → any access to kbB = 403.
#   5. method != tools/call (e.g. tools/list) with scope `r` = 200 (treated as read).
#
# Scoped token format (D44): comma-separated entries, `token|scope1;scope2`,
# scope `kb:<basename>:r|rw`. The KB name is the basename of the dir (kbName).
#
# Expected environment variables: E2E_TMP_DIR, E2E_HTTP_PORT, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# shellcheck source=../lib/assert.sh
source "${E2E_DIR}/lib/assert.sh"
# shellcheck source=../lib/kb.sh
source "${E2E_DIR}/lib/kb.sh"
# shellcheck source=../lib/server.sh
source "${E2E_DIR}/lib/server.sh"

SCENARIO_NAME="10_scoped_tokens"

echo "=== Scenario ${SCENARIO_NAME} ==="

# --- Phase 1: two KBs + scoped tokens ---
KB_A_NAME="${SCENARIO_NAME}-kba"
KB_B_NAME="${SCENARIO_NAME}-kbb"
KB_A_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/${KB_A_NAME}"
KB_B_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/${KB_B_NAME}"

TS="$(date +%s)"
ADMIN_TOKEN="admin-${TS}"
RW_TOKEN="rw-${TS}"
R_TOKEN="r-${TS}"

mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}"
kb_make "$KB_A_DIR"
kb_make "$KB_B_DIR"

# admin (no scope) + rw on kbA + r on kbA. Nobody has scope on kbB except admin.
TOKENS="${ADMIN_TOKEN}, ${RW_TOKEN}|kb:${KB_A_NAME}:rw, ${R_TOKEN}|kb:${KB_A_NAME}:r"

E2E_AUTH=true E2E_TOKENS="$TOKENS" \
    server_start "${KB_A_DIR},${KB_B_DIR}"
server_wait_health 20

trap 'server_stop' EXIT

echo "    kbA name : ${KB_A_NAME}"
echo "    kbB name : ${KB_B_NAME}"

BASE="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"

# http_status <token> <kb> <json-body>  → prints the HTTP status code
http_status() {
    local token="$1" kb="$2" body="$3"
    curl -s -o /dev/null -w "%{http_code}" \
        -X POST "${BASE}?kb=${kb}" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${token}" \
        -d "${body}"
}

# assert_status <expected> <description> <token> <kb> <body>
assert_status() {
    local want="$1" desc="$2" token="$3" kb="$4" body="$5"
    local got
    got="$(http_status "$token" "$kb" "$body")"
    if [[ "$got" == "$want" ]]; then
        _assert_pass "${desc} → HTTP ${got}"
    else
        _assert_fail "${desc} → expected ${want}, got ${got}"
    fi
}

# Body for a read-only tool (atlas_overview) and a write one (map_create).
READ_BODY='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"atlas_overview","arguments":{}}}'
LIST_BODY='{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
write_body() {
    printf '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"scope-%s","title":"Scope %s"}}}' "$1" "$1"
}

echo ""
echo "--- Scope enforcement assertions ---"

# 1. Admin (no scope) = full access.
assert_status 200 "admin: read on kbA"  "$ADMIN_TOKEN" "$KB_A_NAME" "$READ_BODY"
assert_status 200 "admin: write on kbA" "$ADMIN_TOKEN" "$KB_A_NAME" "$(write_body admin)"
assert_status 200 "admin: read on kbB"  "$ADMIN_TOKEN" "$KB_B_NAME" "$READ_BODY"

# 2. Read-only on kbA.
assert_status 200 "r-token: read on kbA"        "$R_TOKEN" "$KB_A_NAME" "$READ_BODY"
assert_status 200 "r-token: tools/list on kbA"  "$R_TOKEN" "$KB_A_NAME" "$LIST_BODY"
assert_status 403 "r-token: write on kbA (403)" "$R_TOKEN" "$KB_A_NAME" "$(write_body rtok)"

# 3. Read-write on kbA.
assert_status 200 "rw-token: read on kbA"  "$RW_TOKEN" "$KB_A_NAME" "$READ_BODY"
assert_status 200 "rw-token: write on kbA" "$RW_TOKEN" "$KB_A_NAME" "$(write_body rwtok)"

# 4. Cross-KB: scope only on kbA → kbB denied.
assert_status 403 "rw-token: read on kbB (cross-KB 403)"  "$RW_TOKEN" "$KB_B_NAME" "$READ_BODY"
assert_status 403 "r-token: read on kbB (cross-KB 403)"   "$R_TOKEN"  "$KB_B_NAME" "$READ_BODY"

# --- Report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
