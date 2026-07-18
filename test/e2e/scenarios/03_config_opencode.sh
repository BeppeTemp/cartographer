#!/usr/bin/env bash
# scenarios/03_config_opencode.sh — OPERATOR scenario: cartographer connect opencode.
#
# Verifies that `cartographer connect opencode` generates opencode.json with the
# correct schema and materializes the bundled skills (via the sync_pull MCP tool)
# into .opencode/skills/.
#
# The client ALWAYS talks to the server over HTTP (no filesystem-only channel, see
# decisions.md): unlike the old cartographer-configure, it therefore requires a
# live server. Does NOT require the agent (no LLM model involved).
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

SCENARIO_NAME="03_config_opencode"

echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
if [[ ! -x "$BIN" ]]; then
    echo "[ERROR] binary not found: ${BIN}" >&2
    exit 1
fi

# --- Create temp KB (empty, --init populates it) and sandbox for the client ---
KB_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/kb"
SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"

mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}"
kb_make "$KB_DIR"
mkdir -p "$SANDBOX_DIR"

server_start "$KB_DIR"
server_wait_health 20
trap 'server_stop' EXIT

# --- Run `cartographer connect opencode` from the sandbox ---
echo "[03] running cartographer connect opencode..."
(cd "$SANDBOX_DIR" && HOME="$SANDBOX_DIR" "$BIN" connect opencode --server-url "http://127.0.0.1:${E2E_HTTP_PORT}/mcp") 2>&1 || true

# --- Assertions ---
echo ""
echo "--- Assertions ---"

OPENCODE_JSON="${SANDBOX_DIR}/opencode.json"
SKILL_FILE="${SANDBOX_DIR}/.opencode/skills/kb-create/SKILL.md"
LOCK_FILE="${SANDBOX_DIR}/.cartographer-sync.lock.json"
CLIENT_CONFIG="${SANDBOX_DIR}/.cartographer.yaml"

# 1. The opencode.json file must exist
assert_file_exists "$OPENCODE_JSON"

# 2. It must contain $schema (official opencode config format)
assert_file_contains "$OPENCODE_JSON" '"$schema"'

# 3. It must contain "enabled": true in the MCP entry
assert_file_contains "$OPENCODE_JSON" '"enabled": true'

# 4. It must contain "type": "remote"
assert_file_contains "$OPENCODE_JSON" '"type": "remote"'

# 5. The bundled kb-create skill must be materialized in .opencode/skills/ (via sync_pull)
assert_file_exists "$SKILL_FILE"

# 6. The v2 lockfile (multi-provider) must exist and reference the opencode provider
assert_file_exists "$LOCK_FILE"
assert_file_contains "$LOCK_FILE" '"opencode"'

# 7. The .cartographer.yaml client config must register the connected provider
assert_file_exists "$CLIENT_CONFIG"
assert_file_contains "$CLIENT_CONFIG" "opencode"

# --- Report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
