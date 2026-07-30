#!/usr/bin/env bash
# scenarios/15_operational_audit.sh — OPERATOR scenario: compliance audit log (D119).
#
# Verifies (operator channel only, curl + CLI — no agent/model):
#   1. An audited tools/call leaves an attempt+completion event pair naming the
#      tool, the KB and the transport.
#   2. `cartographer audit verify` validates the hash chain and reports a
#      non-zero count of valid entries.
#   3. Tampering with a single byte of a recorded entry makes verify fail with a
#      non-zero exit status — the chain is what makes the log evidence.
#   4. `cartographer audit export` refuses to export a chain it cannot verify,
#      and exports JSON containing the recorded events once the log is intact.
#
# The audit log is configured through a YAML config file (E2E_CONFIG) because
# audit.mode/max_segment_bytes have no environment form.
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

SCENARIO_NAME="15_operational_audit"

echo "=== Scenario ${SCENARIO_NAME} ==="

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
KB_NAME="${SCENARIO_NAME}-kb"
KB_DIR="${DIR}/${KB_NAME}"
AUDIT_LOG="${DIR}/audit.jsonl"
CONFIG="${DIR}/config.yaml"
BIN="${REPO_ROOT}/bin/cartographer"

mkdir -p "$DIR"
kb_make "$KB_DIR"

cat > "$CONFIG" <<YAML
http: ":${E2E_HTTP_PORT}"
init: true
kbs:
  - path: ${KB_DIR}
    name: ${KB_NAME}
audit:
  log: ${AUDIT_LOG}
  mode: best_effort
YAML

E2E_CONFIG="$CONFIG" server_start "$KB_DIR"
server_wait_health 20
trap 'server_stop' EXIT

BASE="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"
READ_BODY='{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"atlas_overview","arguments":{}}}'
WRITE_BODY='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"audited","title":"Audited"}}}'

echo ""
echo "--- Phase 1: events are recorded ---"

assert_mcp_ok "audited read succeeds"  "$(mcp_call "$BASE" "$KB_NAME" "" "$READ_BODY")"
assert_mcp_ok "audited write succeeds" "$(mcp_call "$BASE" "$KB_NAME" "" "$WRITE_BODY")"

# Stop the server so the log is closed and fully flushed before inspection.
server_stop
trap - EXIT

assert_file_exists "$AUDIT_LOG"
assert_file_contains "$AUDIT_LOG" '"phase":"attempt"'
assert_file_contains "$AUDIT_LOG" '"phase":"completion"'
assert_file_contains "$AUDIT_LOG" 'map_create'
assert_file_contains "$AUDIT_LOG" 'atlas_overview'
assert_file_contains "$AUDIT_LOG" "$KB_NAME"
assert_file_contains "$AUDIT_LOG" '"transport":"http"'
assert_file_contains "$AUDIT_LOG" '"outcome":"success"'

echo ""
echo "--- Phase 2: the chain verifies ---"

VERIFY_OUT="${DIR}/verify.txt"
if "$BIN" audit verify --log "$AUDIT_LOG" >"$VERIFY_OUT" 2>&1; then
    _assert_pass "audit verify exits 0 on an intact log"
else
    _assert_fail "audit verify exits 0 on an intact log: $(cat "$VERIFY_OUT")"
fi
assert_file_contains "$VERIFY_OUT" "chain verified"

echo ""
echo "--- Phase 3: export ---"

EXPORT_OUT="${DIR}/export.json"
if "$BIN" audit export --log "$AUDIT_LOG" --out "$EXPORT_OUT" >/dev/null 2>&1; then
    _assert_pass "audit export exits 0 on an intact log"
else
    _assert_fail "audit export exits 0 on an intact log"
fi
assert_file_exists "$EXPORT_OUT"
assert_file_contains "$EXPORT_OUT" '"events"'
assert_file_contains "$EXPORT_OUT" 'map_create'

echo ""
echo "--- Phase 4: tampering is detected ---"

# Rewrite the outcome of a recorded entry: the payload stays valid JSON, so only
# the hash chain can catch it.
sed 's/"outcome":"success"/"outcome":"failure"/' "$AUDIT_LOG" > "${AUDIT_LOG}.tampered"
mv "${AUDIT_LOG}.tampered" "$AUDIT_LOG"

TAMPER_OUT="${DIR}/verify-tampered.txt"
if "$BIN" audit verify --log "$AUDIT_LOG" >"$TAMPER_OUT" 2>&1; then
    _assert_fail "audit verify rejects a tampered log: exited 0 on tampered input"
else
    _assert_pass "audit verify rejects a tampered log"
fi

if "$BIN" audit export --log "$AUDIT_LOG" --out "${DIR}/export-tampered.json" >/dev/null 2>&1; then
    _assert_fail "audit export refuses a tampered log: exited 0 on tampered input"
else
    _assert_pass "audit export refuses a tampered log"
fi
assert_file_not_exists "${DIR}/export-tampered.json"

echo ""
if [[ "${E2E_FAILURES}" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
