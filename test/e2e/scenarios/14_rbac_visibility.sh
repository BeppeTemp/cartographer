#!/usr/bin/env bash
# scenarios/14_rbac_visibility.sh — OPERATOR scenario: fine-grained RBAC
# retrieval visibility (D118, GitHub issue #51).
#
# Verifies end-to-end over HTTP (curl — no agent/model):
#   1. A token scoped to a single KB does not see the other KB's concepts in
#      collection tools (search / concept_list / atlas_overview).
#   2. A concept outside a token's perimeter is indistinguishable from a
#      nonexistent one: same generic "not found" text, no title/id leak.
#   3. A write outside a token's perimeter is rejected and leaves the target
#      concept unchanged.
#   4. A no-scope (admin/legacy) token keeps full visibility on every KB —
#      D118 retrocompatibility for pre-existing deployments.
#
# Granularity note: this scenario exercises RBAC at *KB* granularity, the
# only level `cartographer serve` accepts a policy for today
# (`CARTOGRAPHER_TOKENS`/`auth.tokens` compile to `kb:<name>:r|rw` scopes,
# see internal/config/config.go and cmd/cartographer/serve.go:scopedTokens).
# The finer map/journal/type-scoped `auth.Permission` selectors that the
# same resolver (internal/mcpserver/policy.go, internal/auth/auth.go) already
# supports and enforces have no operator-facing YAML/env wiring yet; that
# grain is covered at the Go level in internal/mcpserver/policy_test.go and
# internal/auth/auth_test.go, not here.
#
# All three non-disclosure/denial assertions above collapse to the same
# JSON-RPC shape (D118): HTTP status is always 200 for a well-formed
# request; the outcome is carried in the tool result (`isError` + generic
# `text`), never in the HTTP status code or in a leaked resource name.
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

SCENARIO_NAME="14_rbac_visibility"

echo "=== Scenario ${SCENARIO_NAME} ==="

KB_A_NAME="${SCENARIO_NAME}-kba"
KB_B_NAME="${SCENARIO_NAME}-kbb"
KB_A_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/${KB_A_NAME}"
KB_B_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/${KB_B_NAME}"

TS="$(date +%s)"
ADMIN_TOKEN="admin-${TS}"
A_TOKEN="a-${TS}"

mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}"
kb_make "$KB_A_DIR"
kb_make "$KB_B_DIR"

# admin: no scope (full/legacy access to every KB). a-tok: read-only, scoped
# to kbA only — no rule at all on kbB.
TOKENS="${ADMIN_TOKEN}, ${A_TOKEN}|kb:${KB_A_NAME}:r"

E2E_AUTH=true E2E_TOKENS="$TOKENS" \
    server_start "${KB_A_DIR},${KB_B_DIR}"
server_wait_health 20

trap 'server_stop' EXIT

echo "    kbA name : ${KB_A_NAME}"
echo "    kbB name : ${KB_B_NAME}"

BASE="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"

concept_write_body() {
    printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"%s","frontmatter":{"type":"Note","title":"%s"},"body":"# %s\\nbody text"}}}' "$1" "$2" "$2"
}
concept_read_body() {
    printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"%s"}}}' "$1"
}
search_body() {
    printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"query":"%s"}}}' "$1"
}
CONCEPT_LIST_BODY='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"concept_list","arguments":{}}}'
ATLAS_OVERVIEW_BODY='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"atlas_overview","arguments":{}}}'

echo ""
echo "--- Fixtures: one concept per KB, written by the admin token ---"
assert_mcp_ok "admin: seed notes/hello-a on kbA" "$(mcp_call "$BASE" "$KB_A_NAME" "$ADMIN_TOKEN" "$(concept_write_body notes/hello-a "Hello A")")"
assert_mcp_ok "admin: seed notes/hello-b on kbB" "$(mcp_call "$BASE" "$KB_B_NAME" "$ADMIN_TOKEN" "$(concept_write_body notes/hello-b "Hello B")")"

echo ""
echo "--- 1. Scoped token: full visibility inside its own KB ---"
assert_mcp_ok "a-tok: search on kbA finds its own concept"       "$(mcp_call "$BASE" "$KB_A_NAME" "$A_TOKEN" "$(search_body Hello)")"
RESP="$(mcp_call "$BASE" "$KB_A_NAME" "$A_TOKEN" "$(search_body Hello)")"
assert_mcp_not_discloses "a-tok: search on kbA does not (accidentally) surface kbB's concept" "$RESP" "hello-b"
assert_mcp_ok "a-tok: concept_list on kbA"    "$(mcp_call "$BASE" "$KB_A_NAME" "$A_TOKEN" "$CONCEPT_LIST_BODY")"
assert_mcp_ok "a-tok: atlas_overview on kbA"  "$(mcp_call "$BASE" "$KB_A_NAME" "$A_TOKEN" "$ATLAS_OVERVIEW_BODY")"

echo ""
echo "--- 1bis. Scoped token: no visibility on the other KB (collection tools) ---"
assert_mcp_error_text "a-tok: search on kbB (out of perimeter)"          "$(mcp_call "$BASE" "$KB_B_NAME" "$A_TOKEN" "$(search_body Hello)")" "forbidden"
assert_mcp_error_text "a-tok: concept_list on kbB (out of perimeter)"    "$(mcp_call "$BASE" "$KB_B_NAME" "$A_TOKEN" "$CONCEPT_LIST_BODY")" "forbidden"
assert_mcp_error_text "a-tok: atlas_overview on kbB (out of perimeter)"  "$(mcp_call "$BASE" "$KB_B_NAME" "$A_TOKEN" "$ATLAS_OVERVIEW_BODY")" "forbidden"

echo ""
echo "--- 2. Non-disclosure: a forbidden concept looks nonexistent ---"
RESP="$(mcp_call "$BASE" "$KB_B_NAME" "$A_TOKEN" "$(concept_read_body notes/hello-b)")"
assert_mcp_error_text "a-tok: concept_read notes/hello-b on kbB is generic not-found" "$RESP" "not found"
assert_mcp_not_discloses "a-tok: denied read does not leak the concept's title" "$RESP" "Hello B"

echo ""
echo "--- 3. Write outside the perimeter is rejected and leaves the target unchanged ---"
assert_mcp_error_text "a-tok: concept_write notes/hello-b on kbB is denied" \
    "$(mcp_call "$BASE" "$KB_B_NAME" "$A_TOKEN" "$(concept_write_body notes/hello-b "Hacked")")" "not found"
RESP="$(mcp_call "$BASE" "$KB_B_NAME" "$ADMIN_TOKEN" "$(concept_read_body notes/hello-b)")"
assert_mcp_ok "admin: notes/hello-b on kbB still readable after the denied write" "$RESP"
if echo "$RESP" | grep -qF "Hello B" && ! echo "$RESP" | grep -qF "Hacked"; then
    _assert_pass "kbB concept content unchanged by the denied cross-KB write"
else
    _assert_fail "kbB concept content was mutated by a denied cross-KB write: ${RESP}"
fi

echo ""
echo "--- 4. Admin/legacy token: full retrocompatibility ---"
assert_mcp_ok "admin: search on kbA"          "$(mcp_call "$BASE" "$KB_A_NAME" "$ADMIN_TOKEN" "$(search_body Hello)")"
assert_mcp_ok "admin: search on kbB"          "$(mcp_call "$BASE" "$KB_B_NAME" "$ADMIN_TOKEN" "$(search_body Hello)")"
assert_mcp_ok "admin: concept_read notes/hello-a on kbA" "$(mcp_call "$BASE" "$KB_A_NAME" "$ADMIN_TOKEN" "$(concept_read_body notes/hello-a)")"
assert_mcp_ok "admin: concept_read notes/hello-b on kbB" "$(mcp_call "$BASE" "$KB_B_NAME" "$ADMIN_TOKEN" "$(concept_read_body notes/hello-b)")"

# --- Report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
