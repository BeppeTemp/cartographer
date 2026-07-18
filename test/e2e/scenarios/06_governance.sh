#!/usr/bin/env bash
# scenarios/06_governance.sh — AGENT + governance scenario: supersede and validate.
#
# Flow:
#   1. (Operator) Creates KB, starts server.
#   2. (Agent) Creates two concepts, then supersedes the first with the second.
#      The agent also calls validate to check KB integrity.
#   3. (Operator) Verifies via filesystem that the superseded concept has
#      the expected markers in the frontmatter: status=superseded, superseded_by.
#
# supersede tool (schema verified at runtime):
#   required: [source_id, target_id]
#   optional: reason
#   Effect on source_id: adds to frontmatter
#     status: superseded
#     superseded_by: <target_id>
#     supersede_reason: <reason>
#
# Note on git commits:
#   The MCP tools (snapshot, commit_gate) do NOT create git commits:
#   - snapshot: returns {status:"logged"} — writes to log.md, does not commit
#   - commit_gate: returns {pass:true/false} — pre-commit check, does not commit
#   Only --init creates the initial commit. This limitation is documented here;
#   the commit check is therefore omitted from the scenario for correctness.
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

SCENARIO_NAME="06_governance"

echo "=== Scenario ${SCENARIO_NAME} ==="

# --- Phase 1: create KB and start server ---
KB_NAME="${SCENARIO_NAME}-kb"
KB_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/${KB_NAME}"

mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}"
kb_make "$KB_DIR"

server_start "$KB_DIR"
server_wait_health 20

trap 'server_stop' EXIT

echo "    KB dir : ${KB_DIR}"
echo "    KB name: ${KB_NAME}"

# --- Phase 2: pre-create map and expanded concept via curl (operator) ---
# The agent handles the concepts and governance; the operator creates the base structure.
curl -sf -X POST "http://127.0.0.1:${E2E_HTTP_PORT}/mcp?kb=${KB_NAME}" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"gov","title":"Governance"}}}' \
    >/dev/null

curl -sf -X POST "http://127.0.0.1:${E2E_HTTP_PORT}/mcp?kb=${KB_NAME}" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"gov/test","frontmatter":{"type":"Index","title":"Test"},"body":"# Test\n"}}}' \
    >/dev/null

curl -sf -X POST "http://127.0.0.1:${E2E_HTTP_PORT}/mcp?kb=${KB_NAME}" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"concept_expand","arguments":{"id":"gov/test"}}}' \
    >/dev/null

# --- Phase 3: sandbox and mandate for the agent ---
SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"
sandbox_create "$SANDBOX_DIR" "$KB_NAME"

MANDATE="You are connected to a wiki through the MCP tools of the 'wiki' server. \
Use ONLY the MCP tools — not the local filesystem. \
The map 'gov' and the expanded concept 'gov/test' already exist. \
Perform these steps in order: \
1) Write a concept with id 'gov/test/old-procedure' (concept_write): \
   frontmatter type 'note', title 'Old procedure'; \
   body: 'Original procedure, to be replaced.'. \
2) Write a concept with id 'gov/test/new-procedure' (concept_write): \
   frontmatter type 'note', title 'New procedure'; \
   body: 'Updated procedure that replaces the old one.'. \
3) Supersede 'gov/test/old-procedure' with 'gov/test/new-procedure' \
   (tool supersede, arguments: source_id, target_id and optionally reason). \
4) Call the validate tool to check KB integrity. \
Complete all steps before responding."

# --- Phase 4: run the agent ---
agent_run "$SANDBOX_DIR" "$MANDATE" || true

# --- Phase 5: filesystem assertions ---
echo ""
echo "--- Assertions ---"

OLD_CONCEPT="${KB_DIR}/data/gov/test/old-procedure.md"
NEW_CONCEPT="${KB_DIR}/data/gov/test/new-procedure.md"

# Both concepts must exist
assert_file_exists "$OLD_CONCEPT"
assert_file_exists "$NEW_CONCEPT"

# The superseded concept must have the status in the frontmatter
assert_file_contains "$OLD_CONCEPT" "status: superseded"

# It must contain the pointer to the replacement
assert_file_contains "$OLD_CONCEPT" "superseded_by:"
assert_file_contains "$OLD_CONCEPT" "new-procedure"

# The replacement concept must exist and have the type field
assert_file_contains "$NEW_CONCEPT" "type:"

# --- Phase 6: report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
