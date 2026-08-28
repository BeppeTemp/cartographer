#!/usr/bin/env bash
# scenarios/10_scoped_tokens.sh — OPERATOR scenario: per-KB r/rw scope enforcement (M2, D45; updated D118).
#
# Verifies (operator channel only, curl — no agent/model):
#   1. No-scope (admin) token → full access: read and write on any KB succeed.
#   2. Token with scope `kb:<kbA>:r` (read-only) → read on kbA succeeds, write on kbA is denied.
#   3. Token with scope `kb:<kbA>:rw` → read and write on kbA succeed.
#   4. Cross-KB: token scoped only to kbA → any access to kbB is denied.
#   5. method != tools/call (e.g. tools/list) with scope `r` succeeds (treated as read).
#
# Scoped token format (D44): comma-separated entries, `token|scope1;scope2`,
# scope `kb:<basename>:r|rw`. The KB name is the basename of the dir (kbName).
#
# D118 changed how a denial is carried: the HTTP status is always 200 for a
# well-formed JSON-RPC request; authorization/non-disclosure decisions are
# encoded in the JSON-RPC result (`isError` + generic `text`), not in the
# HTTP status code (there is no more direct HTTP 403 from the RBAC gate —
# 401/Unauthorized from the bearer-token *authentication* layer is
# unaffected and still exercised in 12_mcp_approval.sh and elsewhere).
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

# This scenario asserts on RBAC, not on tool naming: pin the pre-D153 bare names
# so a denial reads as "forbidden" rather than "tool not found". Prefixing has its
# own coverage in 16_prefixed_multikb.sh.
E2E_AUTH=true E2E_TOKENS="$TOKENS" E2E_TOOL_PREFIX_MODE=off \
    server_start "${KB_A_DIR},${KB_B_DIR}"
server_wait_health 20

trap 'server_stop' EXIT

echo "    kbA name : ${KB_A_NAME}"
echo "    kbB name : ${KB_B_NAME}"

BASE="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"

# Body for a read-only tool (atlas_overview) and a write one (map_create).
READ_BODY='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"atlas_overview","arguments":{}}}'
LIST_BODY='{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
write_body() {
    printf '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"scope-%s","title":"Scope %s"}}}' "$1" "$1"
}

echo ""
echo "--- Scope enforcement assertions ---"

# 1. Admin (no scope) = full access.
assert_mcp_ok "admin: read on kbA"  "$(mcp_call "$BASE" "$KB_A_NAME" "$ADMIN_TOKEN" "$READ_BODY")"
assert_mcp_ok "admin: write on kbA" "$(mcp_call "$BASE" "$KB_A_NAME" "$ADMIN_TOKEN" "$(write_body admin)")"
assert_mcp_ok "admin: read on kbB"  "$(mcp_call "$BASE" "$KB_B_NAME" "$ADMIN_TOKEN" "$READ_BODY")"

# 2. Read-only on kbA.
assert_mcp_ok        "r-token: read on kbA"       "$(mcp_call "$BASE" "$KB_A_NAME" "$R_TOKEN" "$READ_BODY")"
assert_mcp_ok        "r-token: tools/list on kbA" "$(mcp_call "$BASE" "$KB_A_NAME" "$R_TOKEN" "$LIST_BODY")"
assert_mcp_error_text "r-token: write on kbA (denied)" "$(mcp_call "$BASE" "$KB_A_NAME" "$R_TOKEN" "$(write_body rtok)")" "forbidden"

# 3. Read-write on kbA.
assert_mcp_ok "rw-token: read on kbA"  "$(mcp_call "$BASE" "$KB_A_NAME" "$RW_TOKEN" "$READ_BODY")"
assert_mcp_ok "rw-token: write on kbA" "$(mcp_call "$BASE" "$KB_A_NAME" "$RW_TOKEN" "$(write_body rwtok)")"

# 4. Cross-KB: scope only on kbA → kbB denied.
assert_mcp_error_text "rw-token: read on kbB (cross-KB, denied)" "$(mcp_call "$BASE" "$KB_B_NAME" "$RW_TOKEN" "$READ_BODY")" "forbidden"
assert_mcp_error_text "r-token: read on kbB (cross-KB, denied)"  "$(mcp_call "$BASE" "$KB_B_NAME" "$R_TOKEN"  "$READ_BODY")" "forbidden"

# --- Report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
