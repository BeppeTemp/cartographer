#!/usr/bin/env bash
# scenarios/02_read_write.sh — AGENT scenario: read + write on kb-homelab-lite.
#
# Verifies that the agent can NAVIGATE the KB (search/concept_read) and then WRITE.
#
# Flow:
#   1. (Operator) Copies the kb-homelab-lite fixture, starts the server.
#   2. (Agent) Looks up the network gateway address, then creates a concept with what it found.
#   3. (Operator) Verifies that the concept exists and contains "10.10.0.1".
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

SCENARIO_NAME="02_read_write"

echo "=== Scenario ${SCENARIO_NAME} ==="

# --- Phase 1: copy fixture and start server ---
KB_NAME="${SCENARIO_NAME}-kb"
KB_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/${KB_NAME}"

mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}"
kb_copy_fixture "kb-homelab-lite" "$KB_DIR"

server_start "$KB_DIR"
server_wait_health 20

trap 'server_stop' EXIT

echo "    KB dir : ${KB_DIR}"
echo "    KB name: ${KB_NAME}"

# --- Phase 2: create sandbox for the agent ---
SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"
sandbox_create "$SANDBOX_DIR" "$KB_NAME"

# --- Phase 3: mandate ---
MANDATE="You are connected to a homelab wiki through the MCP tools of the 'wiki' server. \
Use ONLY the MCP tools — not the local filesystem. \
Perform these steps in order: \
1) Search the wiki for the network gateway address. Use the 'search' tool with query 'gateway', \
   then read the concept found with 'concept_read'. \
2) Create a new concept with id 'infra/rete/gateway-response' (tool concept_write): \
   frontmatter with type 'note' and title 'Gateway response'; \
   body: a sentence reporting the gateway IP address found in step 1. \
   The id must NOT start with 'data/'. \
Complete both steps before responding."

# --- Phase 4: run the agent ---
agent_run "$SANDBOX_DIR" "$MANDATE" || true

# --- Phase 5: assertions ---
echo ""
echo "--- Assertions ---"

CONCEPT_FILE="${KB_DIR}/data/infra/rete/gateway-response.md"

# The concept must exist
assert_file_exists "$CONCEPT_FILE"

# It must contain the type field in the frontmatter
assert_file_contains "$CONCEPT_FILE" "type:"

# It must report the gateway address found in the infra/rete/gateway concept
assert_file_contains "$CONCEPT_FILE" "10.10.0.1"

# --- Phase 6: report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
