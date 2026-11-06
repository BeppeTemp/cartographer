#!/usr/bin/env bash
# scenarios/27_plugin_delivery.sh — a plugin's skill reaches a connected client.
#
# (a) `plugin install <folder> --yes` approves a fixture plugin; the next
#     `sync` materializes its skill for the connected client.
# (b) `status` is in sync, and the lockfile records the source `plugin:<name>`.
# (c) With the plugin folder and the store removed, a sync keeps what is
#     installed: an unreadable plugin retires nothing.
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

SCENARIO_NAME="27_plugin_delivery"

echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
if [[ ! -x "$BIN" ]]; then
    echo "[ERROR] binary not found: ${BIN}" >&2
    exit 1
fi

KB_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/kb"
SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"
PLUGIN_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/team-forks"

mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}" "$SANDBOX_DIR" "${PLUGIN_DIR}/skills/plugin-helper"
kb_make "$KB_DIR"

cat > "${PLUGIN_DIR}/cartographer-plugin.yaml" <<'YAML'
schema: 1
name: team-forks
version: 1.0.0
display_name: Team forks
description: A skill for the team.
cartographer: ">=0.1.0"
YAML
cat > "${PLUGIN_DIR}/skills/plugin-helper/SKILL.md" <<'EOF2'
---
name: plugin-helper
description: Helps with a thing, use it when asked.
---
# Plugin helper
EOF2

server_start "$KB_DIR"
server_wait_health 20
trap 'server_stop' EXIT

SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"
# The plugin store lives in the user cache dir, under HOME: ~/Library/Caches on
# macOS, ~/.cache elsewhere (an inherited XDG_CACHE_HOME would leave the sandbox).
run_cartographer() {
    (cd "$SANDBOX_DIR" && env -u XDG_CACHE_HOME HOME="$SANDBOX_DIR" "$BIN" "$@") 2>&1
}

echo "[27a] connect, install the plugin, sync..."
run_cartographer connect opencode --server-url "$SERVER_URL" || true
run_cartographer plugin install "$PLUGIN_DIR" --yes || true
run_cartographer sync || true

SKILL_FILE="${SANDBOX_DIR}/.config/opencode/skills/plugin-helper/SKILL.md"
echo "--- Assertions (a): the plugin skill is materialized ---"
assert_file_exists "$SKILL_FILE"
assert_file_contains "$SKILL_FILE" "team-forks"
assert_file_contains "${SANDBOX_DIR}/.cartographer-sync.lock.json" "plugin:team-forks"

echo "--- Assertions (b): in sync ---"
if run_cartographer status; then
    _assert_pass "status exits 0 after the plugin sync"
else
    _assert_fail "status is not in sync after the plugin sync"
fi

echo "[27c] the plugin folder and the cache are gone: a sync retires nothing..."
rm -rf "$PLUGIN_DIR" "${SANDBOX_DIR}/.cache/cartographer" "${SANDBOX_DIR}/Library/Caches/cartographer"
run_cartographer sync || true
assert_file_exists "$SKILL_FILE"

echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
