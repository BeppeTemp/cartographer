#!/usr/bin/env bash
# optional/34_server_down_exit_codes.sh — the CLI rows of the acceptance battery that need
# no server: `--help`, and `connect`, `status`, `sync` and `disconnect` with the server down.
#
#   1. `--help` exits 0, lists connect, disconnect, status, sync and doctor, and
#      docs/configurator.md documents each of the five.
#   2. `connect` to an address nobody listens on writes the entry and defers the skills.
#   3. `status` names the unreachable server and exits 2; its JSON says `reachable: false`.
#   4. `sync` exits 2 naming the address and changes no configuration file.
#   5. `disconnect` removes the entry it wrote.
#
# Optional: not in `make e2e`. Runs with HOME and the XDG variables under the scenario's
# directory, never the real client configuration.
#
# Expected environment variables: E2E_TMP_DIR, E2E_HTTP_PORT, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# shellcheck source=../lib/assert.sh
source "${E2E_DIR}/lib/assert.sh"
# shellcheck source=../lib/server.sh
source "${E2E_DIR}/lib/server.sh"

SCENARIO_NAME="34_server_down_exit_codes"
echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
HOME_DIR="${DIR}/home"
mkdir -p "$HOME_DIR"
PORT="$(e2e_free_port)" || exit 1
SERVER_URL="http://127.0.0.1:${PORT}/mcp"

run() {
    (cd "$HOME_DIR" && HOME="$HOME_DIR" XDG_CONFIG_HOME="${HOME_DIR}/.config" XDG_CACHE_HOME="${HOME_DIR}/.cache" \
        XDG_DATA_HOME="${HOME_DIR}/.local/share" XDG_STATE_HOME="${HOME_DIR}/.local/state" \
        CARTOGRAPHER_NO_UPDATE_CHECK=1 "$BIN" "$@")
}

echo ""
echo "--- Phase 1: --help is a success and every listed command is documented ---"
run --help >"${DIR}/help.out" 2>&1; RC=$?
if [[ "$RC" -eq 0 ]]; then _assert_pass "--help exits 0"; else _assert_fail "--help exited ${RC}"; fi
for cmd in connect disconnect status sync doctor; do
    assert_file_contains "${DIR}/help.out" "$cmd"
    if grep -qF -- "cartographer ${cmd}" "${REPO_ROOT}/docs/configurator.md"; then _assert_pass "docs/configurator.md documents ${cmd}"
    else _assert_fail "docs/configurator.md does not document ${cmd}"; fi
done

echo ""
echo "--- Phase 2: connect to a server nobody runs ---"
OUT="$(run connect opencode --server-url "$SERVER_URL" 2>&1)"; RC=$?
if [[ "$RC" -eq 0 ]]; then _assert_pass "connect exits 0"; else _assert_fail "connect exited ${RC}: ${OUT:0:300}"; fi
assert_mcp_contains "the skills are deferred, not dropped" "$OUT" "skill sync deferred"
CONFIG_FILE="${HOME_DIR}/.config/opencode/opencode.json"
assert_file_exists "$CONFIG_FILE"
BEFORE="$(cat "$CONFIG_FILE")"

echo ""
echo "--- Phase 3: status with the server down ---"
OUT="$(run status 2>&1)"; RC=$?
if [[ "$RC" -eq 2 ]]; then _assert_pass "status exits 2"; else _assert_fail "status exited ${RC}, want 2: ${OUT:0:300}"; fi
assert_mcp_contains "status names the unreachable server" "$OUT" "could not reach ${SERVER_URL}"
OUT="$(run status --output json 2>&1)"
assert_mcp_contains "status --output json says reachable false" "$OUT" '"reachable":false'

echo ""
echo "--- Phase 4: sync with the server down ---"
OUT="$(run sync 2>&1)"; RC=$?
if [[ "$RC" -eq 2 ]]; then _assert_pass "sync exits 2"; else _assert_fail "sync exited ${RC}, want 2: ${OUT:0:300}"; fi
assert_mcp_contains "sync names the address" "$OUT" "could not reach http://127.0.0.1:${PORT}/health"
assert_mcp_contains "sync says it changed nothing" "$OUT" "no configuration was modified"
if [[ "$(cat "$CONFIG_FILE")" == "$BEFORE" ]]; then _assert_pass "the client file is unchanged"; else _assert_fail "sync changed ${CONFIG_FILE}"; fi

echo ""
echo "--- Phase 5: disconnect leaves no file ---"
run disconnect opencode >/dev/null 2>&1; RC=$?
if [[ "$RC" -eq 0 ]]; then _assert_pass "disconnect exits 0"; else _assert_fail "disconnect exited ${RC}"; fi
if [[ -f "$CONFIG_FILE" ]] && grep -qF -- "$SERVER_URL" "$CONFIG_FILE"; then _assert_fail "the entry is still in ${CONFIG_FILE}"; else _assert_pass "the entry is gone"; fi

if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
