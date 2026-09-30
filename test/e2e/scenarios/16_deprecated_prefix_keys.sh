#!/usr/bin/env bash
# scenarios/16_deprecated_prefix_keys.sh — OPERATOR scenario: deprecated topology keys (D288).
#
# Until D288 a multi-KB server could prefix each KB's tools (tool_prefix,
# tool_prefix_mode) and opt into a routed mount (mount_mode). The routed mount is
# now the one topology and tools are never prefixed, so those three keys are
# accepted, ignored and warned about — an upgrade must never fail to start.
#
# Verifies (operator channel only, curl — no agent/model):
#   1. A config carrying all three deprecated keys still starts, and the server
#      log names each key once.
#   2. Tools are not prefixed: /health advertises no tool_prefix, the per-KB
#      endpoint answers on the bare tool name and the routed one lists it.
#   3. The routed mount is served although mount_mode says per-kb.
#   4. A KB named like the routed endpoint ("routed") is skipped with a warning
#      to rename it; the other KBs are unaffected.
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

SCENARIO_NAME="16_deprecated_prefix_keys"

echo "=== Scenario ${SCENARIO_NAME} ==="

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
CONFIG="${DIR}/config.yaml"
HOST="http://127.0.0.1:${E2E_HTTP_PORT}"
SERVER_LOG="${E2E_TMP_DIR}/cartographer_e2e.log"

mkdir -p "$DIR"
for name in plainkb prefixedkb routed; do kb_make "${DIR}/${name}"; done

cat > "$CONFIG" <<YAML
http: ":${E2E_HTTP_PORT}"
init: true
mcp:
  mount_mode: per-kb
  tool_prefix_mode: kb-name
kbs:
  - path: ${DIR}/plainkb
    name: plainkb
  - path: ${DIR}/prefixedkb
    name: prefixedkb
    tool_prefix: "zzarb"
  - path: ${DIR}/routed
    name: routed
YAML

E2E_CONFIG="$CONFIG" server_start "${DIR}/plainkb,${DIR}/prefixedkb,${DIR}/routed"
server_wait_health 20
trap 'server_stop' EXIT

echo ""
echo "--- Phase 1: the deprecated keys are accepted and named once ---"

assert_file_contains "$SERVER_LOG" "mcp.mount_mode is deprecated"
assert_file_contains "$SERVER_LOG" "mcp.tool_prefix_mode is deprecated"
assert_file_contains "$SERVER_LOG" 'kbs[].tool_prefix "zzarb" is deprecated'

echo ""
echo "--- Phase 2: tools are never prefixed ---"

HEALTH="${DIR}/health.json"
curl -s "${HOST}/health" -o "$HEALTH"
# The capabilities map still names the (deprecated) gate; what must be gone is a
# per-KB prefix value.
assert_file_not_contains "$HEALTH" '"tool_prefix":"'

call_body() { printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s","arguments":{}}}' "$1"; }
for name in plainkb prefixedkb; do
    assert_mcp_ok "${name}: the bare tool name works on its per-KB endpoint" \
        "$(mcp_call "${HOST}/mcp" "$name" "" "$(call_body atlas_overview)")"
done

echo ""
echo "--- Phase 3: the routed mount is always served ---"

assert_file_contains "$HEALTH" '"mount_mode":"routed"'
assert_file_contains "$HEALTH" '"routed_path":"/mcp/routed"'
LIST="${DIR}/routed-tools.json"
curl -s -X POST "${HOST}/mcp/routed" -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' > "$LIST"
assert_file_contains "$LIST" '"name":"atlas_overview"'

echo ""
echo "--- Phase 4: a KB named like the routed endpoint is skipped ---"

assert_file_contains "$SERVER_LOG" 'KB "routed"'
assert_file_contains "$SERVER_LOG" "rename the KB"
assert_file_contains "$HEALTH" '"name":"plainkb"'
assert_file_not_contains "$HEALTH" '"name":"routed"'

echo ""
if [[ "${E2E_FAILURES}" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
