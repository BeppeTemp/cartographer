#!/usr/bin/env bash
# scenarios/04_skill_lifecycle.sh — OPERATOR scenario: skill lifecycle (materialization + prune).
#
# Uses the kb-homelab-lite fixture, which contains the domain skill kbinfra--query-rete.
# Verifies that:
#   (a) `cartographer connect opencode --auto-trust` materializes the skill in
#       .opencode/skills/<name>/SKILL.md (via sync_pull) and writes the v2 lockfile.
#   (b) After the skill is removed from the KB (server unchanged — sync_pull reads
#       the KB filesystem on every call), `cartographer sync` prunes it from the
#       client.
#
# Requires a live server (the client ALWAYS talks over HTTP, see decisions.md).
# Does NOT require the agent.
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

SCENARIO_NAME="04_skill_lifecycle"

echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
if [[ ! -x "$BIN" ]]; then
    echo "[ERROR] binary not found: ${BIN}" >&2
    exit 1
fi

# --- Setup: copy fixture with domain skill, start server ---
KB_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/kb"
SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"

mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}"
kb_copy_fixture "kb-homelab-lite" "$KB_DIR"
mkdir -p "$SANDBOX_DIR"

server_start "$KB_DIR"
server_wait_health 20
trap 'server_stop' EXIT

SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"
SKILL_NAME="kbinfra--query-rete"
SKILL_SRC="${KB_DIR}/skills/${SKILL_NAME}"
SKILL_DEST="${SANDBOX_DIR}/.opencode/skills/${SKILL_NAME}/SKILL.md"
LOCK_FILE="${SANDBOX_DIR}/.cartographer-sync.lock.json"

# --- (a) initial connect with --auto-trust ---
echo "[04a] cartographer connect opencode --auto-trust..."
(cd "$SANDBOX_DIR" && HOME="$SANDBOX_DIR" "$BIN" connect opencode --server-url "$SERVER_URL" --auto-trust) 2>&1 || true

echo ""
echo "--- Assertions (a): materialization ---"

# The domain skill must be materialized
assert_file_exists "$SKILL_DEST"

# The lockfile must exist and reference the skill
assert_file_exists "$LOCK_FILE"
assert_file_contains "$LOCK_FILE" "$SKILL_NAME"

# --- (b) Remove the skill from the KB and resync ---
echo ""
echo "[04b] removing skill from KB and cartographer sync..."
rm -rf "$SKILL_SRC"

(cd "$SANDBOX_DIR" && HOME="$SANDBOX_DIR" "$BIN" sync --auto-trust) 2>&1 || true

echo ""
echo "--- Assertions (b): prune ---"

# The skill must have been pruned from the client
if [[ -f "$SKILL_DEST" ]]; then
    _assert_fail "skill '${SKILL_NAME}' not pruned from ${SKILL_DEST}"
else
    _assert_pass "skill '${SKILL_NAME}' correctly pruned from .opencode/skills/"
fi

# The lockfile must still exist (updated)
assert_file_exists "$LOCK_FILE"

# The lockfile must NOT list the removed skill anymore
if grep -q "$SKILL_NAME" "$LOCK_FILE" 2>/dev/null; then
    _assert_fail "lockfile still contains '${SKILL_NAME}' after prune"
else
    _assert_pass "lockfile no longer contains '${SKILL_NAME}'"
fi

# --- Report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
