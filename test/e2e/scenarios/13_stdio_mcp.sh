#!/usr/bin/env bash
# D116 trusted stdio MCP lifecycle across every provider; the command is never executed.
set -uo pipefail
SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"
source "${E2E_DIR}/lib/assert.sh"; source "${E2E_DIR}/lib/kb.sh"; source "${E2E_DIR}/lib/server.sh"
SCENARIO_NAME="13_stdio_mcp"; BIN="${REPO_ROOT}/bin/cartographer"; KB_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/kb"; SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"; CONFIG="${E2E_TMP_DIR}/${SCENARIO_NAME}/server.yaml"; SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"
# The sentinel is the resolved value of the referenced variable: it proves the
# descriptor travels as a reference and no provider file ever materializes it.
SENTINEL="D116_LOCAL_SECRET_MUST_NOT_APPEAR"
write_config() { cat >"${CONFIG}" <<EOF
http: ":${E2E_HTTP_PORT}"
init: true
kbs:
  - path: ${KB_DIR}
    mcp_allowlist:
      - name: local-tools
        transport: stdio
        target: ${FAKE}
EOF
}
mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}" "${SANDBOX_DIR}"; SANDBOX_REAL="$(cd "${SANDBOX_DIR}" && pwd)"; FAKE="${SANDBOX_REAL}/fake-mcp"; MARKER="${SANDBOX_REAL}/executed"
kb_make "${KB_DIR}"; mkdir -p "${KB_DIR}/mcp"
# A real executable that leaves a marker if anything ever starts it.
printf '#!/bin/sh\nprintf launched > %s\n' "${MARKER}" >"${FAKE}"; chmod 755 "${FAKE}"; export LOCAL_TOKEN="${SENTINEL}"
descriptor() { printf '{"type":"stdio","command":"%s","args":["serve","--port","%s"],"env":{"TOKEN":"${LOCAL_TOKEN}"}}\n' "${FAKE}" "$1" >"${KB_DIR}/mcp/local-tools.json"; }
descriptor 39273; write_config; export E2E_CONFIG="${CONFIG}"
server_start "${KB_DIR}"; server_wait_health 20; trap 'server_stop' EXIT
CLAUDE="${SANDBOX_DIR}/.claude.json"; CODEX="${SANDBOX_DIR}/.codex/config.toml"; OPENCODE="${SANDBOX_DIR}/opencode.json"; KIRO="${SANDBOX_DIR}/.kiro/settings/mcp.json"; LOCK="${SANDBOX_DIR}/.cartographer-sync.lock.json"

# 1. Connect every provider, approve the descriptor and sync it.
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" connect --agents claude,codex,kiro,opencode --server-url "${SERVER_URL}") >/dev/null
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" approve mcp local-tools --kb kb --yes) >/dev/null
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" sync) >/dev/null

# 2. Every provider receives command, args and the env reference in its native shape.
assert_file_contains "${CLAUDE}" '"command": "'"${FAKE}"'"'; assert_file_contains "${CLAUDE}" '"serve",'; assert_file_contains "${CLAUDE}" '"--port",'; assert_file_contains "${CLAUDE}" '"39273"'; assert_file_contains "${CLAUDE}" '"${LOCAL_TOKEN}"'
assert_file_contains "${CODEX}" 'command = "'"${FAKE}"'"'; assert_file_contains "${CODEX}" 'args = ["serve", "--port", "39273"]'; assert_file_contains "${CODEX}" '"TOKEN" = "${LOCAL_TOKEN}"'
assert_file_contains "${OPENCODE}" '"type": "local"'; assert_file_contains "${OPENCODE}" '"command": ['; assert_file_contains "${OPENCODE}" '"{env:LOCAL_TOKEN}"'
assert_file_contains "${KIRO}" '"command": "'"${FAKE}"'"'; assert_file_contains "${KIRO}" '"serve",'; assert_file_contains "${KIRO}" '"--port",'; assert_file_contains "${KIRO}" '"39273"'; assert_file_contains "${KIRO}" '"TOKEN": "${LOCAL_TOKEN}"'

# 3. The command was never started and the secret was never materialized.
assert_file_not_exists "${MARKER}"
assert_file_not_contains "${CLAUDE}" "${SENTINEL}"; assert_file_not_contains "${CODEX}" "${SENTINEL}"; assert_file_not_contains "${OPENCODE}" "${SENTINEL}"; assert_file_not_contains "${KIRO}" "${SENTINEL}"; assert_file_not_contains "${LOCK}" "${SENTINEL}"

# 4. Changing any descriptor field invalidates the D115 approval (here: args).
descriptor 39274
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" sync) >"${SANDBOX_DIR}/stale.out"
assert_file_contains "${SANDBOX_DIR}/stale.out" 'needs_approval'; assert_file_not_contains "${CLAUDE}" 'local-tools'; assert_file_not_contains "${LOCK}" 'local-tools'

# 5. Re-approving restores it, revoking prunes it from every provider and the lock.
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" approve mcp local-tools --kb kb --yes) >/dev/null
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" sync) >/dev/null
assert_file_contains "${CLAUDE}" '"39274"'
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" approve revoke mcp local-tools --kb kb) >/dev/null
(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" sync) >/dev/null
assert_file_not_contains "${CLAUDE}" 'local-tools'; assert_file_not_contains "${CODEX}" 'mcp_servers.local-tools'; assert_file_not_contains "${OPENCODE}" 'local-tools'; assert_file_not_contains "${KIRO}" 'local-tools'; assert_file_not_contains "${LOCK}" 'local-tools'
assert_file_not_exists "${MARKER}"

if [[ "${E2E_FAILURES}" -eq 0 ]]; then echo "[SCENARIO ${SCENARIO_NAME}] PASS"; exit 0; fi; exit 1
