#!/usr/bin/env bash
# scenarios/17_routed_multikb.sh — OPERATOR scenario: the routed topology (D187, D288).
#
# A three-KB server. The routed mount is the one agent-facing topology: a client
# using N KBs stops paying N copies of the same tool schemas in its fixed context
# on every model round-trip, and since D288 a provider's binding travels in the
# URL (?kbs=). The point of this scenario is that all of it reaches the wire,
# which the unit tests cannot show.
#
# Verifies (operator channel only, curl + CLI — no agent/model):
#   1. /health advertises mount_mode and routed_path, so a client can detect the
#      topology instead of assuming it.
#   2. /mcp/routed advertises each tool exactly once and its payload is far
#      smaller than the per-KB mounts summed.
#   3. A call routed with `kb` reaches that KB; a call without `kb` is refused
#      naming the mounted KBs, and one with an unknown `kb` is refused too.
#   4. The per-KB endpoints still answer exactly as before.
#   5. `cartographer connect` writes ONE MCP entry per provider, pointed at
#      /mcp/routed, with ?kbs= carrying an explicit binding.
#   6. A provider bound to 2 of 3 KBs sees a 2-value enum and is refused the
#      third; a provider bound to 1 KB sees no `kb` property at all and calls
#      without it; an unknown name in ?kbs= is a 400.
#   7. From an old per-KB client state, ONE `sync` leaves a single entry and no
#      orphan per-KB entry.
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
if grep -q 'not available' <<< "$UNKNOWN_KB"; then
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
echo "--- Phase 5: one MCP entry per provider, the binding in the URL ---"

run_client() {
    local out="$1"; shift
    (cd "$SANDBOX" && HOME="$SANDBOX" "$BIN" "$@") >"$out" 2>&1
}

# opencode is bound to two of the three KBs, claude to one.
run_client "${DIR}/connect-opencode.log" connect opencode --server-url "$SERVER_URL" --kb alpha,beta --auto-trust || true
run_client "${DIR}/connect-claude.log" connect claude --server-url "$SERVER_URL" --kb gamma --auto-trust || true

OPENCODE_CFG="${SANDBOX}/opencode.json"
if [[ ! -f "$OPENCODE_CFG" ]]; then
    OPENCODE_CFG="$(find "$SANDBOX" -name 'opencode.json' -print -quit 2>/dev/null)"
fi
CLAUDE_CFG="${SANDBOX}/.claude.json"
assert_file_exists "$OPENCODE_CFG"
assert_file_exists "$CLAUDE_CFG"
assert_file_contains "$OPENCODE_CFG" "/mcp/routed?kbs=alpha,beta"
assert_file_contains "$CLAUDE_CFG" "/mcp/routed?kbs=gamma"
assert_file_not_contains "$OPENCODE_CFG" "kb=alpha\""

ENTRIES="$(grep -o '/mcp/routed' "$OPENCODE_CFG" | wc -l | tr -d ' ')"
if [[ "$ENTRIES" == "1" ]]; then
    _assert_pass "exactly one routed MCP entry was written for opencode"
else
    _assert_fail "${ENTRIES} routed MCP entries were written for opencode, want 1"
fi

echo ""
echo "--- Phase 6: what each binding sees on the wire ---"

TWO_URL="${ROUTED_URL}?kbs=alpha,beta"
ONE_URL="${ROUTED_URL}?kbs=gamma"

TWO_LIST="${DIR}/two-tools.json"
post "$TWO_URL" "$list_body" > "$TWO_LIST"
assert_file_contains "$TWO_LIST" '"enum":["alpha","beta"]'
assert_file_not_contains "$TWO_LIST" 'gamma'
THIRD="$(post "$TWO_URL" "$(call_with_kb gamma)")"
if grep -q '"isError":true' <<< "$THIRD" && grep -q 'not available' <<< "$THIRD"; then
    _assert_pass "a KB outside the binding is refused on the 2-KB connection"
else
    _assert_fail "the third KB was not refused on the 2-KB connection: ${THIRD}"
fi
assert_mcp_ok "a KB inside the binding answers on the 2-KB connection" "$(post "$TWO_URL" "$(call_with_kb beta)")"

ONE_LIST="${DIR}/one-tools.json"
post "$ONE_URL" "$list_body" > "$ONE_LIST"
assert_file_not_contains "$ONE_LIST" '"kb":{'
assert_mcp_ok "a 1-KB connection dispatches a call with no kb" \
    "$(post "$ONE_URL" '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"atlas_overview","arguments":{}}}')"
OTHER="$(post "$ONE_URL" "$(call_with_kb alpha)")"
if grep -q '"isError":true' <<< "$OTHER"; then
    _assert_pass "a KB other than the bound one is refused on the 1-KB connection"
else
    _assert_fail "another KB was not refused on the 1-KB connection: ${OTHER}"
fi

BAD_CODE="$(curl -s -o /dev/null -w '%{http_code}' -X POST "${ROUTED_URL}?kbs=alpha,nope" \
    -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" -d "$list_body")"
if [[ "$BAD_CODE" == "400" ]]; then
    _assert_pass "an unknown name in ?kbs= is refused with 400"
else
    _assert_fail "an unknown name in ?kbs= returned ${BAD_CODE}, want 400"
fi

echo ""
echo "--- Phase 7: one sync heals an old per-KB client state ---"

# Put opencode back into the pre-D288 shape: the bare entry on the plain
# endpoint plus one ?kb= entry per KB. No reconnect follows, only a sync.
python3 - "$OPENCODE_CFG" "$SERVER_URL" <<'PY'
import json, sys
path, url = sys.argv[1], sys.argv[2]
cfg = json.load(open(path))
cfg["mcp"]["cartographer"]["url"] = url
for kb in ("alpha", "beta", "gamma"):
    cfg["mcp"]["cartographer-" + kb] = {"enabled": True, "type": "remote", "url": url + "?kb=" + kb}
json.dump(cfg, open(path, "w"), indent=2)
PY
if run_client "${DIR}/sync.log" sync; then
    _assert_pass "cartographer sync succeeds from the old per-KB state"
else
    _assert_fail "cartographer sync failed: $(cat "${DIR}/sync.log")"
fi
assert_file_contains "$OPENCODE_CFG" "/mcp/routed?kbs=alpha,beta"
assert_file_not_contains "$OPENCODE_CFG" "cartographer-alpha"
assert_file_not_contains "$OPENCODE_CFG" "cartographer-gamma"
ENTRIES="$(python3 -c "import json,sys; print(len(json.load(open(sys.argv[1]))['mcp']))" "$OPENCODE_CFG")"
if [[ "$ENTRIES" == "1" ]]; then
    _assert_pass "one sync left exactly one entry and no orphan"
else
    _assert_fail "${ENTRIES} entries after sync, want 1"
fi

echo ""
if [[ "${E2E_FAILURES}" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
