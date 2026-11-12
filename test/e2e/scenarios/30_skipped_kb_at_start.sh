#!/usr/bin/env bash
# scenarios/30_skipped_kb_at_start.sh — OPERATOR scenario: #1963, a KB that cannot be
# mounted does not stop the server.
#
# A healthy KB, a --kb path that is missing (moved) and a kbs[] remote that cannot be
# cloned. Verifies, with curl and the binary only:
#   1. The server starts and serves the healthy KB; the log has one ERROR line per
#      skipped KB naming the cause, and the missing path is not shown to clients.
#   2. /health lists both as skipped_kbs with a reason (the clone one as retrying), next
#      to the healthy one in kbs.
#   3. kb_status on a skipped KB, through the routed endpoint, says it is not mounted and why.
#   4. `cartographer status` against the server names the skipped KBs.
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

SCENARIO_NAME="30_skipped_kb_at_start"
echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
CONFIG="${DIR}/config.yaml"
LOG="${DIR}/server.log"
HOST="http://127.0.0.1:${E2E_HTTP_PORT}"
mkdir -p "$DIR/data"
kb_make "${DIR}/kb-ok"
kb_bootstrap "${DIR}/kb-ok"

cat > "$CONFIG" <<YAML
http: "127.0.0.1:${E2E_HTTP_PORT}"
data: "${DIR}/data"
kbs:
  - remote: "file://${DIR}/nowhere.git"
    name: kb-remote
YAML

CARTOGRAPHER_AUTH=false CARTOGRAPHER_TOKENS= \
CARTOGRAPHER_CREDENTIALS_DIR="${DIR}/credentials" CARTOGRAPHER_AUDIT_LOG="${DIR}/audit/audit.log" \
    "$BIN" serve --config "$CONFIG" --kb "${DIR}/kb-ok,${DIR}/kb-moved" >"$LOG" 2>&1 &
_SERVER_PID_FILE="${DIR}/server.pid"
echo "$!" > "$_SERVER_PID_FILE"
trap 'server_stop' EXIT
server_wait_health 20

echo ""
echo "--- Phase 1: the server is up and says why it skipped two KBs ---"
assert_file_contains "$LOG" 'ERROR: KB "kb-moved"'
assert_file_contains "$LOG" "path missing: moved? update kbs[].path"
assert_file_contains "$LOG" 'ERROR: KB "kb-remote"'

echo ""
echo "--- Phase 2: /health ---"
HEALTH="$(curl -s "${HOST}/health")"
assert_mcp_contains "the healthy KB is mounted" "$HEALTH" '"name":"kb-ok"'
assert_mcp_contains "the moved KB is listed as skipped" "$HEALTH" '"skipped_kbs":[{"name":"kb-moved","reason":"path missing: moved? update kbs[].path"}'
assert_mcp_contains "the remote KB is listed as skipped and retried" "$HEALTH" '"name":"kb-remote"'
assert_mcp_contains "its clone is retried" "$HEALTH" '"retrying":true'
assert_mcp_not_discloses "no server path in /health" "$HEALTH" "${DIR}"

echo ""
echo "--- Phase 3: kb_status on a skipped KB ---"
STATUS="$(curl -s -X POST "${HOST}/mcp/routed" -H 'content-type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kb_status","arguments":{"kb":"kb-moved"}}}')"
assert_mcp_contains "kb_status says the KB is not mounted" "$STATUS" "is not mounted on this server"
assert_mcp_contains "kb_status says why" "$STATUS" "path missing: moved? update kbs[].path"

echo ""
echo "--- Phase 4: cartographer status ---"
CLIENT_HOME="${DIR}/home"
mkdir -p "$CLIENT_HOME"
(cd "$CLIENT_HOME" && HOME="$CLIENT_HOME" "$BIN" connect opencode --server-url "${HOST}/mcp") >/dev/null 2>&1 || true
STATUS_OUT="$(cd "$CLIENT_HOME" && HOME="$CLIENT_HOME" "$BIN" status 2>&1 || true)"
assert_mcp_contains "status names the moved KB" "$STATUS_OUT" "KB not mounted: kb-moved"

if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
