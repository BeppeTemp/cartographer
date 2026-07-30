#!/usr/bin/env bash
# D115 allow-list and hash-bound MCP approval.
set -uo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"
source "${E2E_DIR}/lib/assert.sh"; source "${E2E_DIR}/lib/kb.sh"; source "${E2E_DIR}/lib/server.sh"
SCENARIO_NAME="12_mcp_approval"; BIN="${REPO_ROOT}/bin/cartographer"; KB_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/kb"; SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"; CONFIG="${E2E_TMP_DIR}/${SCENARIO_NAME}/server.yaml"; SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"
assert_file_absent_contains() { if [[ ! -f "$1" ]] || ! grep -qF "$2" "$1"; then _assert_pass "file '$1' does not contain '$2'"; else _assert_fail "file '$1' unexpectedly contains '$2'"; fi; }
write_config() { cat >"${CONFIG}" <<EOF
http: ":${E2E_HTTP_PORT}"
init: true
kbs:
  - path: ${KB_DIR}
    mcp_allowlist:
      - name: wiki-tools
        transport: http
        target: https://tools.example.com/mcp
EOF
}
mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}" "${SANDBOX_DIR}"; kb_make "${KB_DIR}"; mkdir -p "${KB_DIR}/mcp"
printf '%s\n' '{"type":"http","url":"https://tools.example.com/mcp"}' >"${KB_DIR}/mcp/wiki-tools.json"; write_config; export E2E_CONFIG="${CONFIG}"
server_start "${KB_DIR}"; server_wait_health 20; trap 'server_stop' EXIT
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" connect claude --server-url "${SERVER_URL}" --auto-trust) >/dev/null
CLAUDE="${SANDBOX_DIR}/.claude.json"; LOCK="${SANDBOX_DIR}/.cartographer-sync.lock.json"
assert_file_absent_contains "${CLAUDE}" 'wiki-tools'
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" approve mcp wiki-tools --kb kb --yes) >/dev/null
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" sync) >/dev/null
assert_file_contains "${CLAUDE}" 'wiki-tools'; assert_file_contains "${LOCK}" 'wiki-tools'
printf '%s\n' '{"type":"http","url":"https://tools.example.com/new"}' >"${KB_DIR}/mcp/wiki-tools.json"
sed -i.bak 's#https://tools.example.com/mcp#https://tools.example.com/new#' "${CONFIG}"; rm -f "${CONFIG}.bak"; server_stop; server_start "${KB_DIR}"; server_wait_health 20
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" sync) >"${SANDBOX_DIR}/stale.out"
assert_file_contains "${SANDBOX_DIR}/stale.out" 'needs_approval'; assert_file_absent_contains "${CLAUDE}" 'wiki-tools'; assert_file_absent_contains "${LOCK}" 'wiki-tools'
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" approve mcp wiki-tools --kb kb --yes) >/dev/null
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" sync) >/dev/null
assert_file_contains "${CLAUDE}" 'https://tools.example.com/new'; assert_file_contains "${LOCK}" 'wiki-tools'
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" approve revoke mcp wiki-tools --kb kb) >/dev/null
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" sync) >/dev/null
assert_file_absent_contains "${CLAUDE}" 'wiki-tools'; assert_file_absent_contains "${LOCK}" 'wiki-tools'
if [[ "${E2E_FAILURES}" -eq 0 ]]; then echo "[SCENARIO ${SCENARIO_NAME}] PASS"; exit 0; fi; exit 1
