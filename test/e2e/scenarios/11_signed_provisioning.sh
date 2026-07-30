#!/usr/bin/env bash
# scenarios/11_signed_provisioning.sh — signed provisioning and fail-closed rotation.
set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"
source "${E2E_DIR}/lib/assert.sh"
source "${E2E_DIR}/lib/kb.sh"
source "${E2E_DIR}/lib/server.sh"

SCENARIO_NAME="11_signed_provisioning"
BIN="${REPO_ROOT}/bin/cartographer"
KB_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/kb"
SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"
CONFIG="${E2E_TMP_DIR}/${SCENARIO_NAME}/server.yaml"
SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"
FIRST_SEED="9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
FIRST_PUBLIC="d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"
SECOND_SEED="4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb"

write_config() { cat >"${CONFIG}" <<EOF
http: ":${E2E_HTTP_PORT}"
init: true
kbs:
  - path: ${KB_DIR}
    artifact_signing_seed: $1
EOF
}

mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}" "${SANDBOX_DIR}"
kb_make "${KB_DIR}"
mkdir -p "${KB_DIR}/skills/signed"
cat >"${KB_DIR}/skills/signed/SKILL.md" <<'EOF'
---
name: signed
description: Signed provisioning E2E fixture
---
signed content
EOF
write_config "${FIRST_SEED}"
export E2E_CONFIG="${CONFIG}"
server_start "${KB_DIR}"
server_wait_health 20
trap 'server_stop' EXIT

if (cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" connect claude --server-url "${SERVER_URL}" --pin-key "kb=${FIRST_PUBLIC}") >"${SANDBOX_DIR}/connect.out" 2>&1; then _assert_pass "signed connect succeeds"; else _assert_fail "signed connect fails: $(cat "${SANDBOX_DIR}/connect.out")"; fi
PROVIDER_FILE="${SANDBOX_DIR}/.claude/skills/signed/SKILL.md"
LOCK_FILE="${SANDBOX_DIR}/.cartographer-sync.lock.json"
assert_file_exists "${PROVIDER_FILE}"
assert_file_exists "${LOCK_FILE}"
cp "${PROVIDER_FILE}" "${SANDBOX_DIR}/provider.before"
cp "${LOCK_FILE}" "${SANDBOX_DIR}/lock.before"

server_stop
write_config "${SECOND_SEED}"
server_start "${KB_DIR}"
server_wait_health 20
if sync_output=$(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" sync 2>&1); then _assert_fail "sync unexpectedly accepts artifacts signed by an unpinned key"; else _assert_pass "sync rejects artifacts signed by an unpinned key"; [[ "${sync_output}" == *"unknown signing key"* ]] && _assert_pass "unknown key error is actionable" || _assert_fail "missing actionable unknown-key error: ${sync_output}"; fi
cmp -s "${SANDBOX_DIR}/provider.before" "${PROVIDER_FILE}" && _assert_pass "failed signed sync leaves provider file unchanged" || _assert_fail "failed signed sync changed provider file"
cmp -s "${SANDBOX_DIR}/lock.before" "${LOCK_FILE}" && _assert_pass "failed signed sync leaves lockfile unchanged" || _assert_fail "failed signed sync changed lockfile"
[[ "${E2E_FAILURES}" -eq 0 ]] && exit 0
exit 1
