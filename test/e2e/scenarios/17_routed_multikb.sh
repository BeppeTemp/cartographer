#!/usr/bin/env bash
# scenarios/17_routed_multikb.sh — OPERATOR scenario: routed multi-KB mount (D187).
#
# A three-KB server with mcp.mount_mode: routed. The point of the mode is that a
# client using N KBs stops paying N copies of the same tool schemas in its fixed
# context on every model round-trip; the point of this scenario is that the flag
# actually reaches the wire, which the unit tests cannot show.
#
# Verifies (operator channel only, curl + CLI — no agent/model):
#   1. /health advertises mount_mode and routed_path, so a client can detect the
#      topology instead of assuming it.
#   2. /mcp/routed advertises each tool exactly once and its payload is far
#      smaller than the per-KB mounts summed.
#   3. A call routed with `kb` reaches that KB; a call without `kb` is refused
#      naming the mounted KBs, and one with an unknown `kb` is refused too.
#   4. The per-KB endpoints still answer exactly as before.
#   5. `cartographer connect` writes ONE MCP entry, pointed at /mcp/routed.
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

SCENARIO_NAME="17_routed_multikb"

echo "=== Scenario ${SCENARIO_NAME} ==="

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
CONFIG="${DIR}/config.yaml"
SANDBOX="${DIR}/home"
BIN="${REPO_ROOT}/bin/cartographer"
HOST="http://127.0.0.1:${E2E_HTTP_PORT}"
SERVER_URL="${HOST}/mcp"
ROUTED_URL="${HOST}/mcp/routed"

KBS=(alpha beta gamma)
mkdir -p "$DIR" "$SANDBOX"
for name in "${KBS[@]}"; do kb_make "${DIR}/${name}"; done

{
    echo "http: \":${E2E_HTTP_PORT}\""
    echo "init: true"
    echo "mcp:"
    echo "  mount_mode: routed"
    echo "kbs:"
    for name in "${KBS[@]}"; do
        echo "  - path: ${DIR}/${name}"
        echo "    name: ${name}"
    done
} > "$CONFIG"

KB_CSV="$(IFS=,; echo "${KBS[*]/#/${DIR}/}")"
E2E_CONFIG="$CONFIG" server_start "$KB_CSV"
server_wait_health 20
trap 'server_stop' EXIT

echo ""
echo "--- Phase 1: the topology is discoverable from /health ---"

HEALTH="${DIR}/health.json"
curl -s "${HOST}/health" -o "$HEALTH"
assert_file_contains "$HEALTH" '"mount_mode":"routed"'
assert_file_contains "$HEALTH" '"routed_path":"/mcp/routed"'

echo ""
echo "--- Phase 2: one copy of the tools, and a much smaller payload ---"

list_body='{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
post() {
    curl -s -X POST "$1" -H "Content-Type: application/json" \
        -H "Accept: application/json, text/event-stream" -d "$2"
}

ROUTED_LIST="${DIR}/routed-tools.json"
post "$ROUTED_URL" "$list_body" > "$ROUTED_LIST"
PERKB_LIST="${DIR}/perkb-tools.json"
post "${SERVER_URL}?kb=alpha" "$list_body" > "$PERKB_LIST"

# atlas_overview must appear exactly once on the routed mount.
OCCURRENCES="$(grep -o '"name":"atlas_overview"' "$ROUTED_LIST" | wc -l | tr -d ' ')"
if [[ "$OCCURRENCES" == "1" ]]; then
    _assert_pass "atlas_overview is advertised exactly once on the routed mount"
else
    _assert_fail "atlas_overview advertised ${OCCURRENCES} times on the routed mount, want 1"
fi

# Every tool schema carries the kb argument, required with 3 KBs routed.
assert_file_contains "$ROUTED_LIST" '"kb"'

ROUTED_BYTES="$(wc -c < "$ROUTED_LIST" | tr -d ' ')"
PERKB_BYTES="$(wc -c < "$PERKB_LIST" | tr -d ' ')"
THREE_MOUNTS=$((PERKB_BYTES * 3))
echo "[info] tools/list: routed ${ROUTED_BYTES} bytes vs ${THREE_MOUNTS} for three per-KB mounts"
if [[ "$ROUTED_BYTES" -lt $((PERKB_BYTES * 2)) ]]; then
    _assert_pass "the routed payload is well under two copies of the per-KB one"
else
    _assert_fail "routed payload ${ROUTED_BYTES} is not smaller than two per-KB copies (${PERKB_BYTES} each)"
fi

echo ""
echo "--- Phase 3: kb selects the target, and is never inferred ---"

routed_call() {
    post "$ROUTED_URL" "$1"
}
call_with_kb() {
    printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"atlas_overview","arguments":{"kb":"%s"}}}' "$1"
}

for name in "${KBS[@]}"; do
    assert_mcp_ok "routed call with kb=${name} succeeds" "$(routed_call "$(call_with_kb "$name")")"
done

NO_KB="$(routed_call '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"atlas_overview","arguments":{}}}')"
if grep -q '"isError":true' <<< "$NO_KB" && grep -q 'alpha' <<< "$NO_KB" && grep -q 'gamma' <<< "$NO_KB"; then
    _assert_pass "a routed call with no kb is refused, naming the mounted KBs"
else
    _assert_fail "a routed call with no kb was not refused with the KB list: ${NO_KB}"
fi

UNKNOWN_KB="$(routed_call "$(call_with_kb nope)")"
if grep -q 'unknown kb' <<< "$UNKNOWN_KB"; then
    _assert_pass "a routed call with an unknown kb is refused"
else
    _assert_fail "an unknown kb was not refused: ${UNKNOWN_KB}"
fi

# The KB travels in the arguments here: a ?kb= on this URL is a second channel.
CONFLICT_CODE="$(curl -s -o /dev/null -w '%{http_code}' -X POST "${ROUTED_URL}?kb=alpha" \
    -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -d "$list_body")"
if [[ "$CONFLICT_CODE" == "400" ]]; then
    _assert_pass "?kb= on the routed endpoint is refused with 400"
else
    _assert_fail "?kb= on the routed endpoint returned ${CONFLICT_CODE}, want 400"
fi

echo ""
echo "--- Phase 4: the per-KB endpoints are untouched ---"

for name in "${KBS[@]}"; do
    assert_mcp_ok "per-KB endpoint ?kb=${name} still answers" \
        "$(mcp_call "$SERVER_URL" "$name" "" '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"atlas_overview","arguments":{}}}')"
done
# The per-KB list has no kb argument: routing added an endpoint, it changed none.
if grep -q '"kb"' "$PERKB_LIST"; then
    _assert_fail "the per-KB tools/list gained a kb argument: the routed mount is not additive"
else
    _assert_pass "the per-KB tools/list is unchanged (no kb argument)"
fi

echo ""
echo "--- Phase 5: the client writes ONE MCP entry, at the routed endpoint ---"

(cd "$SANDBOX" && HOME="$SANDBOX" "$BIN" connect opencode --server-url "$SERVER_URL" --kb all --auto-trust) \
    >"${DIR}/connect.log" 2>&1 || true

OPENCODE_CFG="${SANDBOX}/.config/opencode/opencode.json"
if [[ ! -f "$OPENCODE_CFG" ]]; then
    OPENCODE_CFG="$(find "$SANDBOX" -name 'opencode.json' -print -quit 2>/dev/null)"
fi
assert_file_exists "$OPENCODE_CFG"
assert_file_contains "$OPENCODE_CFG" "/mcp/routed"
assert_file_not_contains "$OPENCODE_CFG" "kb=alpha"

ENTRIES="$(grep -o '/mcp/routed' "$OPENCODE_CFG" | wc -l | tr -d ' ')"
if [[ "$ENTRIES" == "1" ]]; then
    _assert_pass "exactly one routed MCP entry was written"
else
    _assert_fail "${ENTRIES} routed MCP entries were written, want 1"
fi

echo ""
if [[ "${E2E_FAILURES}" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
