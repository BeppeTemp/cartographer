#!/usr/bin/env bash
# scenarios/01_mcp_crud.sh — basic CRUD scenario via MCP tools.
#
# Flow:
#   1. (Operator) Creates its own temp KB, starts the server.
#   2. (Agent) Creates a map, expands a concept into an expanded concept and writes
#      a satellite concept via MCP tools.
#   3. (Operator) Verifies the state on the filesystem.
#
# Expected environment variables: E2E_MODEL, E2E_TMP_DIR, E2E_HTTP_PORT, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# Load libs
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

SCENARIO_NAME="01_mcp_crud"

echo "=== Scenario ${SCENARIO_NAME} ==="

# --- Phase 1: create KB and start server ---
KB_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/kb"
KB_NAME="${SCENARIO_NAME}-kb"

# Use a subdirectory dedicated to the scenario for the server's tmp files
mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}"

# Rename the KB directory to have a recognizable KB name
mkdir -p "${KB_DIR%/*}"
kb_make "${E2E_TMP_DIR}/${SCENARIO_NAME}/${KB_NAME}"
KB_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/${KB_NAME}"

server_start "$KB_DIR"
server_wait_health 20

# Stop the server at the end of the scenario (EXIT = any exit)
trap 'server_stop' EXIT

echo "    KB dir : ${KB_DIR}"
echo "    KB name: ${KB_NAME}"

# --- Phase 2: create the sandbox for the agent ---
SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"
sandbox_create "$SANDBOX_DIR" "$KB_NAME"

# --- Phase 3: build the mandate ---
# The mandate is natural language; the agent must use ONLY the MCP tools.
MANDATE="You are connected to a wiki through the MCP tools of the 'wiki' server. Work ONLY with the MCP tools \
(do not touch the local filesystem). Before each call, read the tool's schema to know which \
arguments are required. Perform these steps in order: \
1) Create a map with short name 'note' and title 'Note' (tool map_create). \
2) Write a concept with id 'note/test-session' (tool concept_write): frontmatter type 'note', \
   title 'Test session'; body 'Test session container.'. \
3) Expand that concept into an expanded concept, so it can grow with satellite concepts (tool \
   concept_expand, id 'note/test-session'). \
4) Write a note (concept) with id 'note/test-session/first-note' (tool concept_write): \
   in the frontmatter set the mandatory field type to 'note' and a readable title; \
   as body write 'First E2E test — write via MCP.'. \
   The id must NOT start with 'data/'. \
5) Call atlas_overview and briefly summarize the structure created. \
Complete all five steps before responding."

# --- Phase 4: run the agent ---
# opencode's exit code is informational; the filesystem assertions are the real oracle.
agent_run "$SANDBOX_DIR" "$MANDATE" || true

# --- Phase 5: operator assertions on the oracle (filesystem) ---
# Note: the CRUD scenario checks the state on the filesystem. Git versioning
# (commit_gate/commit) is an explicit operation separate from writes,
# covered by a dedicated governance scenario — not here.
echo ""
echo "--- Assertions ---"

# OKF layout note: the KB mounts maps under <root>/data/<map>/<expanded-concept>/.
DATA_DIR="${KB_DIR}/data"
CONCEPT_FILE="${DATA_DIR}/note/test-session/first-note.md"

# 5a. The 'note' map must exist as a directory
assert_dir_exists "${DATA_DIR}/note" "map 'note' created"

# 5b. The expanded concept 'test-session' must exist inside 'note'
assert_dir_exists "${DATA_DIR}/note/test-session" "expanded concept 'test-session' created"

# 5c. A concept matching pattern *first-note* with YAML frontmatter must exist
assert_concept_exists "${DATA_DIR}/note/test-session" "*first-note*"

# 5d. The frontmatter must contain the mandatory 'type' field
assert_file_contains "$CONCEPT_FILE" "type:"

# 5e. The body must contain the content required by the mandate
assert_file_contains "$CONCEPT_FILE" "First E2E test"

# --- Phase 6: scenario report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
