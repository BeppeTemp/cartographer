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
# The signing seed is the KB's file of the credentials directory (D905), and a
# kbs[] entry names a remote (D903): the KB is cloned from a local bare
# repository into the data directory.
SEED_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/seed"
BARE_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/kb.git"
DATA_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/data"
SANDBOX_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/sandbox"
CONFIG="${E2E_TMP_DIR}/${SCENARIO_NAME}/server.yaml"
CREDENTIALS_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}/credentials"
SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"
FIRST_SEED="9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
FIRST_PUBLIC="d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"
SECOND_SEED="4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb"

# write_seed rotates the key: the file is read at start.
write_seed() { (umask 077 && printf '%s\n' "$1" >"${CREDENTIALS_DIR}/kb.signing"); }

mkdir -p "${E2E_TMP_DIR}/${SCENARIO_NAME}" "${SANDBOX_DIR}"
mkdir -m 700 -p "${CREDENTIALS_DIR}"
cat >"${CONFIG}" <<EOF
http: "127.0.0.1:${E2E_HTTP_PORT}"
data: ${DATA_DIR}
kbs:
  - remote: file://${BARE_DIR}
    name: kb
EOF
kb_make "${SEED_DIR}"
mkdir -p "${SEED_DIR}/skills/signed"
cat >"${SEED_DIR}/skills/signed/SKILL.md" <<'EOF'
---
name: signed
description: Signed provisioning E2E fixture
---
signed content
EOF
# The remote's KB is a real one: serve never creates it (D906).
kb_bootstrap "${SEED_DIR}"
git init --bare "${BARE_DIR}" >/dev/null 2>&1
git -C "${BARE_DIR}" symbolic-ref HEAD refs/heads/main
git -C "${SEED_DIR}" init -q
git -C "${SEED_DIR}" symbolic-ref HEAD refs/heads/main
git -C "${SEED_DIR}" -c user.name=e2e -c user.email=e2e@example.com add -A
git -C "${SEED_DIR}" -c user.name=e2e -c user.email=e2e@example.com commit -q -m "seed"
git -C "${SEED_DIR}" push -q "${BARE_DIR}" main
write_seed "${FIRST_SEED}"
export E2E_CONFIG="${CONFIG}" E2E_CREDENTIALS_DIR="${CREDENTIALS_DIR}"
server_start ""
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
write_seed "${SECOND_SEED}"
server_start ""
server_wait_health 20
if sync_output=$(cd "${SANDBOX_DIR}" && HOME="${SANDBOX_DIR}" "${BIN}" sync 2>&1); then _assert_fail "sync unexpectedly accepts artifacts signed by an unpinned key"; else _assert_pass "sync rejects artifacts signed by an unpinned key"; [[ "${sync_output}" == *"unknown signing key"* ]] && _assert_pass "unknown key error is actionable" || _assert_fail "missing actionable unknown-key error: ${sync_output}"; fi
cmp -s "${SANDBOX_DIR}/provider.before" "${PROVIDER_FILE}" && _assert_pass "failed signed sync leaves provider file unchanged" || _assert_fail "failed signed sync changed provider file"
cmp -s "${SANDBOX_DIR}/lock.before" "${LOCK_FILE}" && _assert_pass "failed signed sync leaves lockfile unchanged" || _assert_fail "failed signed sync changed lockfile"
[[ "${E2E_FAILURES}" -eq 0 ]] && exit 0
exit 1
