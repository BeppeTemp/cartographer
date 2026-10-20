#!/usr/bin/env bash
# scenarios/20_origin_and_listen.sh — OPERATOR scenario: the fixed server rules of D906.
#
# Verifies, with curl and the binary only:
#   1. A request with a cross-origin Origin header is refused (403); one whose
#      Origin has the request's own host:port, and one with no Origin header
#      (a non-browser client), are accepted.
#   2. A tool hidden from tools/list (reindex, advanced) is served when named.
#   3. `serve --http 0.0.0.0:<port>` with auth off refuses to start and says
#      why, while the loopback address the other scenarios use starts.
#   4. `serve` on a folder that is not a KB fails naming `kb create`.
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

SCENARIO_NAME="20_origin_and_listen"
echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
KB_DIR="${DIR}/kbo"
mkdir -p "$DIR"
kb_make "$KB_DIR"

export E2E_AUTH=false
server_start "$KB_DIR"
server_wait_health 20
trap 'server_stop' EXIT

MCP="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"
PING='{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
code_with_origin() {
    local origin="$1"
    if [[ -n "$origin" ]]; then
        curl -s -o /dev/null -w '%{http_code}' -X POST "$MCP" -H 'content-type: application/json' -H "Origin: ${origin}" -d "$PING"
    else
        curl -s -o /dev/null -w '%{http_code}' -X POST "$MCP" -H 'content-type: application/json' -d "$PING"
    fi
}

[[ "$(code_with_origin "https://evil.example.net")" == "403" ]] && _assert_pass "a cross-origin request is refused" || _assert_fail "a cross-origin request was not refused"
[[ "$(code_with_origin "http://127.0.0.1:${E2E_HTTP_PORT}")" == "200" ]] && _assert_pass "a same-origin request is accepted" || _assert_fail "a same-origin request was refused"
[[ "$(code_with_origin "")" == "200" ]] && _assert_pass "a request with no Origin header is accepted" || _assert_fail "a request with no Origin header was refused"

LIST="$(curl -s -X POST "$MCP" -H 'content-type: application/json' -d "$PING")"
if printf '%s' "$LIST" | grep -q '"reindex"'; then _assert_fail "an advanced tool is listed"; else _assert_pass "an advanced tool is not listed"; fi
CALL="$(curl -s -X POST "$MCP" -H 'content-type: application/json' \
    -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"reindex","arguments":{}}}')"
if printf '%s' "$CALL" | grep -q 'tool not found'; then _assert_fail "an unlisted advanced tool is refused: ${CALL}"; else _assert_pass "an unlisted advanced tool is callable by name"; fi

# --- Off-loopback with auth off ---
PORT_X="$((E2E_HTTP_PORT + 1))"
if CARTOGRAPHER_AUTH=false CARTOGRAPHER_TOKENS= CARTOGRAPHER_CREDENTIALS_DIR="${DIR}/cred-x" CARTOGRAPHER_AUDIT_LOG="${DIR}/audit-x/audit.log" \
    "$BIN" serve --kb "$KB_DIR" --http "0.0.0.0:${PORT_X}" >"${DIR}/refused.log" 2>&1; then
    _assert_fail "serve started on 0.0.0.0 with auth off"
else
    _assert_pass "serve refuses 0.0.0.0 with auth off"
fi
assert_file_contains "${DIR}/refused.log" "authentication off"

# --- A folder that is not a KB ---
mkdir -p "${DIR}/not-a-kb"
if CARTOGRAPHER_AUTH=false CARTOGRAPHER_CREDENTIALS_DIR="${DIR}/cred-y" CARTOGRAPHER_AUDIT_LOG="${DIR}/audit-y/audit.log" \
    "$BIN" serve --kb "${DIR}/not-a-kb" --http "127.0.0.1:$((E2E_HTTP_PORT + 2))" >"${DIR}/missing.log" 2>&1; then
    _assert_fail "serve mounted a folder that is not a KB"
else
    _assert_pass "serve refuses a folder that is not a KB"
fi
assert_file_contains "${DIR}/missing.log" "cartographer kb create"

if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
