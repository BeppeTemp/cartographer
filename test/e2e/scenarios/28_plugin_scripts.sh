#!/usr/bin/env bash
# scenarios/28_plugin_scripts.sh — a plugin's scripts: command, sync event, tool.
#
# The executable form of the tutorial in docs/plugins.md, on the example plugin
# test/e2e/fixtures/plugin-example (isolated HOME, no real client):
#   (a) `plugin validate` and `plugin install` from the folder; the command is
#       not dispatched before approval.
#   (b) `plugin approve`, `plugin config`, then the command prints the setting.
#   (c) `sync` fires the sync.after handler (a marker file); `sync --dry-run` does not.
#   (d) the plugin's tool answers through `cartographer call` (the stdio bridge's
#       runtime), under the provider's scope.
#   (e) one byte changed in the stored plugin.star: the plugin is no longer
#       active and the command stops.
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

SCENARIO_NAME="28_plugin_scripts"

echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
if [[ ! -x "$BIN" ]]; then
    echo "[ERROR] binary not found: ${BIN}" >&2
    exit 1
fi

KB_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/kb"
SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"
PLUGIN_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/example-forks"
OUT="${E2E_TMP_DIR}/${SCENARIO_NAME}/out.txt"

mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}" "$SANDBOX_DIR"
kb_make "$KB_DIR"
cp -R "${REPO_ROOT}/test/e2e/fixtures/plugin-example" "$PLUGIN_DIR"

server_start "$KB_DIR"
server_wait_health 20
trap 'server_stop' EXIT

SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"
# The plugin store lives in the user cache dir, under HOME (an inherited
# XDG_CACHE_HOME would leave the sandbox).
run_cartographer() {
    (cd "$SANDBOX_DIR" && env -u XDG_CACHE_HOME HOME="$SANDBOX_DIR" "$BIN" "$@") >"$OUT" 2>&1
    local rc=$?
    cat "$OUT"
    return $rc
}
MARKER="${SANDBOX_DIR}/.example-forks/last-sync.txt"

echo "[28a] validate, connect, install without approving..."
run_cartographer plugin validate "$PLUGIN_DIR" || _assert_fail "plugin validate failed"
assert_file_contains "$OUT" "command example-forks hello"
assert_file_contains "$OUT" "tool example_forks_echo"
run_cartographer connect claude --server-url "$SERVER_URL" || true
run_cartographer plugin install "$PLUGIN_DIR" </dev/null || true
run_cartographer example-forks hello
RC=$?
if [[ $RC -eq 2 ]]; then
    _assert_pass "the command is not dispatched before approval (exit 2)"
else
    _assert_fail "the command ran or failed otherwise before approval (exit ${RC})"
fi
assert_file_contains "$OUT" "installed but not approved"

echo "[28b] approve, configure, run the command..."
# The test is the user here: it passes the explicit consent flag.
run_cartographer plugin approve example-forks --yes || _assert_fail "plugin approve failed"
run_cartographer plugin config example-forks greeting=hi || _assert_fail "plugin config failed"
run_cartographer example-forks hello ada || _assert_fail "the command failed"
assert_file_contains "$OUT" "hi, ada!"

echo "[28c] sync fires the event, a dry run does not..."
run_cartographer sync || true
assert_file_exists "$MARKER"
rm -f "$MARKER"
run_cartographer sync --dry-run || true
assert_file_not_exists "$MARKER"

echo "[28d] the tool answers through the bridge's runtime..."
# Needs plugin-tools-bridge (cartographer call serving plugin tools).
run_cartographer call example_forks_echo --provider claude --json '{"kb":"kb","text":"hello"}' || _assert_fail "cartographer call example_forks_echo failed"
assert_file_contains "$OUT" '"echo"'

echo "[28e] one byte changed in the stored script: the plugin stops..."
STORED="$(find "$SANDBOX_DIR" -path '*plugins/example-forks/*/plugin.star' -type f | head -n 1)"
if [[ -z "$STORED" ]]; then
    _assert_fail "the stored plugin.star was not found"
else
    chmod u+w "$STORED"
    printf '#' >>"$STORED"
    run_cartographer example-forks hello
    RC=$?
    if [[ $RC -ne 0 ]]; then
        _assert_pass "the command stops once the stored tree is modified (exit ${RC})"
    else
        _assert_fail "the command still runs from a modified tree"
    fi
    assert_file_not_contains "$OUT" "hi, ada!"
fi

echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
