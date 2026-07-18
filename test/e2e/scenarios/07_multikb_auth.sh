#!/usr/bin/env bash
# scenarios/07_multikb_auth.sh — AGENT + auth scenario: multi-KB and bearer token.
#
# Verifies:
#   1. With a valid token, the agent writes to kbA (via the Authorization header).
#   2. The concept exists in kbA and NOT in kbB (KB isolation).
#   3. (Operator) Without a token, the same MCP call via curl gets 401.
#
# Architecture:
#   - Two KBs (kbA, kbB) mounted on the same server.
#   - CARTOGRAPHER_AUTH=true CARTOGRAPHER_TOKENS=<tok> enable auth.
#   - The opencode sandbox points at ?kb=kbA with an Authorization header.
#   - Primary oracle = filesystem (kbA has the concept, kbB does not).
#   - Secondary oracle = HTTP 401 for requests without a token.
#
# Expected environment variables: E2E_MODEL, E2E_TMP_DIR, E2E_HTTP_PORT, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# shellcheck source=../lib/assert.sh
source "${E2E_DIR}/lib/assert.sh"
# shellcheck source=../lib/kb.sh
source "${E2E_DIR}/lib/kb.sh"
# shellcheck source=../lib/server.sh
source "${E2E_DIR}/lib/server.sh"
# shellcheck source=../lib/sandbox.sh
source "${E2E_DIR}/lib/sandbox.sh"
# shellcheck source=../lib/agent.sh
source "${E2E_DIR}/lib/agent.sh"

SCENARIO_NAME="07_multikb_auth"

echo "=== Scenario ${SCENARIO_NAME} ==="

# --- Phase 1: create two KBs and start server with auth ---
KB_A_NAME="${SCENARIO_NAME}-kba"
KB_B_NAME="${SCENARIO_NAME}-kbb"
KB_A_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/${KB_A_NAME}"
KB_B_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/${KB_B_NAME}"
AUTH_TOKEN="e2e-auth-token-$(date +%s)"

mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}"
kb_make "$KB_A_DIR"
kb_make "$KB_B_DIR"

# Start server with both KBs and authentication enabled
E2E_AUTH=true E2E_TOKENS="$AUTH_TOKEN" \
    server_start "${KB_A_DIR},${KB_B_DIR}"
server_wait_health 20

trap 'server_stop' EXIT

echo "    kbA dir  : ${KB_A_DIR}"
echo "    kbB dir  : ${KB_B_DIR}"
echo "    kbA name : ${KB_A_NAME}"
echo "    kbB name : ${KB_B_NAME}"

# --- Phase 2: pre-create structure in kbA via curl with token ---
curl -sf -X POST "http://127.0.0.1:${E2E_HTTP_PORT}/mcp?kb=${KB_A_NAME}" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"auth-test","title":"Auth Test"}}}' \
    >/dev/null

curl -sf -X POST "http://127.0.0.1:${E2E_HTTP_PORT}/mcp?kb=${KB_A_NAME}" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"auth-test/isolation","frontmatter":{"type":"Index","title":"Isolation"},"body":"# Isolation\n"}}}' \
    >/dev/null

curl -sf -X POST "http://127.0.0.1:${E2E_HTTP_PORT}/mcp?kb=${KB_A_NAME}" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_expand","arguments":{"id":"auth-test/isolation"}}}' \
    >/dev/null

# --- Phase 3: verify 401 without a token (auth test, operator channel) ---
echo ""
echo "--- Auth assertion: 401 without token ---"
HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "http://127.0.0.1:${E2E_HTTP_PORT}/mcp?kb=${KB_A_NAME}" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"atlas_overview","arguments":{}}}')

if [[ "$HTTP_STATUS" == "401" ]]; then
    _assert_pass "request without token receives HTTP 401"
else
    _assert_fail "request without token should receive 401, got: ${HTTP_STATUS}"
fi

# --- Phase 4: sandbox for the agent with auth header ---
SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"
sandbox_create_auth "$SANDBOX_DIR" "$KB_A_NAME" "$AUTH_TOKEN"

# --- Phase 5: mandate for the agent ---
MANDATE="You are connected to the 'kbA' wiki through the MCP tools of the 'wiki' server (with authentication). \
Use ONLY the MCP tools — not the local filesystem. \
The map 'auth-test' and the expanded concept 'auth-test/isolation' already exist in kbA. \
Write a concept with id 'auth-test/isolation/isolation-check' (tool concept_write): \
frontmatter type 'note', title 'KB isolation check'; \
body: 'This concept must exist only in kbA, not in kbB.'. \
Then call atlas_overview to confirm the structure."

# --- Phase 6: run the agent ---
agent_run "$SANDBOX_DIR" "$MANDATE" || true

# --- Phase 7: filesystem assertions ---
echo ""
echo "--- Filesystem assertions ---"

CONCEPT_KBA="${KB_A_DIR}/data/auth-test/isolation/isolation-check.md"
CONCEPT_KBB="${KB_B_DIR}/data/auth-test/isolation/isolation-check.md"

# The concept must exist in kbA
assert_file_exists "$CONCEPT_KBA"

# The concept must NOT exist in kbB (KB isolation)
if [[ -f "$CONCEPT_KBB" ]]; then
    _assert_fail "concept found in kbB — KB isolation violation"
else
    _assert_pass "concept NOT present in kbB (correct KB isolation)"
fi

# The concept in kbA must have the type field
assert_file_contains "$CONCEPT_KBA" "type:"

# --- Phase 8: report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
