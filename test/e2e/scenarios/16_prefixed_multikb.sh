#!/usr/bin/env bash
# scenarios/16_prefixed_multikb.sh — OPERATOR scenario: prefixed multi-KB installation (D120).
#
# A two-KB server where ONE KB carries an arbitrary explicit tool_prefix (D102)
# and the other does not. Before D120 the client re-derived the prefix locally,
# so every client-owned direct tool call against the prefixed KB failed and was
# reported as "server unreachable" / "mcp-config missing".
#
# Verifies (operator channel only, curl + CLI — no agent/model):
#   1. /health advertises the effective per-KB tool prefix, so the namespace is
#      discoverable without re-deriving it client-side.
#   2. The prefixed KB answers on its prefixed tool names and NOT on the bare
#      ones; the unprefixed KB is unchanged (the D102 default-off promise).
#   3. `cartographer connect` writes one MCP entry per KB.
#   4. `status`, `sync` and remote `reindex` succeed against both KBs and emit
#      no false unreachable/missing diagnostic.
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

SCENARIO_NAME="16_prefixed_multikb"

echo "=== Scenario ${SCENARIO_NAME} ==="

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
KB_PLAIN="plainkb"
KB_PREFIXED="prefixedkb"
# Deliberately unrelated to the KB name: the client must not be able to guess it.
PREFIX="zzarb"
KB_PLAIN_DIR="${DIR}/${KB_PLAIN}"
KB_PREFIXED_DIR="${DIR}/${KB_PREFIXED}"
CONFIG="${DIR}/config.yaml"
SANDBOX="${DIR}/home"
BIN="${REPO_ROOT}/bin/cartographer"
SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"

mkdir -p "$DIR" "$SANDBOX"
kb_make "$KB_PLAIN_DIR"
kb_make "$KB_PREFIXED_DIR"

cat > "$CONFIG" <<YAML
http: ":${E2E_HTTP_PORT}"
init: true
kbs:
  - path: ${KB_PLAIN_DIR}
    name: ${KB_PLAIN}
  - path: ${KB_PREFIXED_DIR}
    name: ${KB_PREFIXED}
    tool_prefix: "${PREFIX}"
YAML

# Phases 1-3 assert on the mixed prefixed/unprefixed shape, so they pin the
# pre-D153 mode: with the kb-name default both KBs would be prefixed and there
# would be no bare name and no collision to observe. The new default has its own
# phase at the end.
E2E_TOOL_PREFIX_MODE=off E2E_CONFIG="$CONFIG" server_start "${KB_PLAIN_DIR},${KB_PREFIXED_DIR}"
server_wait_health 20
trap 'server_stop' EXIT

BASE="$SERVER_URL"

echo ""
echo "--- Phase 1: the prefix is discoverable from /health ---"

HEALTH="${DIR}/health.json"
curl -s "http://127.0.0.1:${E2E_HTTP_PORT}/health" -o "$HEALTH"
assert_file_contains "$HEALTH" '"tool_prefix":"'"${PREFIX}"'"'
assert_file_contains "$HEALTH" "$KB_PREFIXED"
assert_file_contains "$HEALTH" "$KB_PLAIN"

echo ""
echo "--- Phase 2: tools answer on the advertised namespace ---"

call_body() { printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s","arguments":{}}}' "$1"; }

assert_mcp_ok "unprefixed KB: bare tool name works" \
    "$(mcp_call "$BASE" "$KB_PLAIN" "" "$(call_body atlas_overview)")"
assert_mcp_ok "prefixed KB: prefixed tool name works" \
    "$(mcp_call "$BASE" "$KB_PREFIXED" "" "$(call_body "${PREFIX}__atlas_overview")")"

# The bare name must NOT resolve on the prefixed KB: that is the failure D120
# was reported for, and the reason the client must discover the prefix.
BARE_ON_PREFIXED="$(mcp_call "$BASE" "$KB_PREFIXED" "" "$(call_body atlas_overview)")"
if echo "$BARE_ON_PREFIXED" | grep -q '"isError":true'; then
    _assert_pass "prefixed KB: bare tool name does not resolve"
else
    _assert_fail "prefixed KB: bare tool name unexpectedly resolved: ${BARE_ON_PREFIXED}"
fi

echo ""
echo "--- Phase 3: client connect writes one entry per KB ---"

(cd "$SANDBOX" && HOME="$SANDBOX" "$BIN" connect opencode --server-url "$SERVER_URL" --auto-trust) >"${DIR}/connect.log" 2>&1 || true

OPENCODE_CFG="${SANDBOX}/.config/opencode/opencode.json"
if [[ ! -f "$OPENCODE_CFG" ]]; then
    OPENCODE_CFG="$(find "$SANDBOX" -name 'opencode.json' -print -quit 2>/dev/null)"
fi
assert_file_exists "$OPENCODE_CFG"
assert_file_contains "$OPENCODE_CFG" "$KB_PLAIN"
assert_file_contains "$OPENCODE_CFG" "$KB_PREFIXED"

echo ""
echo "--- Phase 4: status/sync/reindex report no false diagnostic ---"

run_client() {
    local out="$1"; shift
    (cd "$SANDBOX" && HOME="$SANDBOX" "$BIN" "$@") >"$out" 2>&1
    return $?
}

STATUS_OUT="${DIR}/status.log"
run_client "$STATUS_OUT" status || true
assert_file_not_contains "$STATUS_OUT" "server unreachable"
assert_file_not_contains "$STATUS_OUT" "mcp-config  missing"

SYNC_OUT="${DIR}/sync.log"
if run_client "$SYNC_OUT" sync; then
    _assert_pass "cartographer sync exits 0 against a prefixed multi-KB server"
else
    _assert_fail "cartographer sync failed: $(cat "$SYNC_OUT")"
fi
assert_file_not_contains "$SYNC_OUT" "unreachable"

REINDEX_OUT="${DIR}/reindex.log"
if run_client "$REINDEX_OUT" reindex; then
    _assert_pass "remote reindex exits 0 on both KBs"
else
    _assert_fail "remote reindex failed: $(cat "$REINDEX_OUT")"
fi
assert_file_not_contains "$REINDEX_OUT" "unreachable"
assert_file_not_contains "$REINDEX_OUT" "tool not found"

echo ""
echo "--- Phase 5: the server warns only when 2+ KBs collide (D144) ---"

SERVER_LOG="${E2E_TMP_DIR}/cartographer_e2e.log"
COLLISION_MARKER="register identical MCP tool names"

# One prefixed + one unprefixed KB is unambiguous: no warning.
assert_file_not_contains "$SERVER_LOG" "$COLLISION_MARKER"

# Same two KBs, both mounted unprefixed: the server names them.
server_stop
CONFIG_PLAIN="${DIR}/config-plain.yaml"
cat > "$CONFIG_PLAIN" <<YAML
http: ":${E2E_HTTP_PORT}"
init: true
kbs:
  - path: ${KB_PLAIN_DIR}
    name: ${KB_PLAIN}
  - path: ${KB_PREFIXED_DIR}
    name: ${KB_PREFIXED}
YAML
E2E_TOOL_PREFIX_MODE=off E2E_CONFIG="$CONFIG_PLAIN" server_start "${KB_PLAIN_DIR},${KB_PREFIXED_DIR}"
server_wait_health 20

assert_file_contains "$SERVER_LOG" "$COLLISION_MARKER"
assert_file_contains "$SERVER_LOG" "\"${KB_PLAIN}\""
assert_file_contains "$SERVER_LOG" "\"${KB_PREFIXED}\""

# The warning changes nothing: both KBs still answer on the bare tool names.
assert_mcp_ok "unprefixed mount: ${KB_PLAIN} answers on the bare tool name" \
    "$(mcp_call "$BASE" "$KB_PLAIN" "" "$(call_body atlas_overview)")"
assert_mcp_ok "unprefixed mount: ${KB_PREFIXED} answers on the bare tool name" \
    "$(mcp_call "$BASE" "$KB_PREFIXED" "" "$(call_body atlas_overview)")"

echo ""
echo "--- Phase 4: the kb-name default prefixes every KB, and says so (D153) ---"

# Same two KBs, no mcp.tool_prefix_mode anywhere: the default applies.
server_stop
E2E_CONFIG="$CONFIG_PLAIN" server_start "${KB_PLAIN_DIR},${KB_PREFIXED_DIR}"
server_wait_health 20

DERIVED_PLAIN="$(printf '%s' "$KB_PLAIN" | tr 'A-Z' 'a-z' | tr -c 'a-z0-9_' '_' | sed 's/__*/_/g; s/^_//; s/_$//')"
DERIVED_PREFIXED="$(printf '%s' "$KB_PREFIXED" | tr 'A-Z' 'a-z' | tr -c 'a-z0-9_' '_' | sed 's/__*/_/g; s/^_//; s/_$//')"

# No collision to warn about any more: both KBs are prefixed.
assert_file_not_contains "$SERVER_LOG" "$COLLISION_MARKER"
# A derived prefix is announced, because adding a KB renames the others' tools.
assert_file_contains "$SERVER_LOG" "derived from the KB name"

HEALTH_DEFAULT="${DIR}/health-default.json"
curl -s "http://127.0.0.1:${E2E_HTTP_PORT}/health" -o "$HEALTH_DEFAULT"
assert_file_contains "$HEALTH_DEFAULT" '"tool_prefix":"'"${DERIVED_PLAIN}"'"'

assert_mcp_ok "kb-name default: ${KB_PLAIN} answers on its derived prefixed name" \
    "$(mcp_call "$BASE" "$KB_PLAIN" "" "$(call_body "${DERIVED_PLAIN}__atlas_overview")")"
assert_mcp_ok "kb-name default: ${KB_PREFIXED} answers on its derived prefixed name" \
    "$(mcp_call "$BASE" "$KB_PREFIXED" "" "$(call_body "${DERIVED_PREFIXED}__atlas_overview")")"

BARE_UNDER_DEFAULT="$(mcp_call "$BASE" "$KB_PLAIN" "" "$(call_body atlas_overview)")"
if grep -q "tool not found" <<< "$BARE_UNDER_DEFAULT"; then
    _assert_pass "kb-name default: the bare tool name no longer resolves"
else
    _assert_fail "kb-name default: bare tool name unexpectedly resolved: ${BARE_UNDER_DEFAULT}"
fi

echo ""
if [[ "${E2E_FAILURES}" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
