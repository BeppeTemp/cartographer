#!/usr/bin/env bash
# optional/32_secrets_lifecycle.sh — the secrets rows of the acceptance battery: a real
# age key, a missing one and a wrong one.
#
#   1. `kb secrets init` on a KB whose server is not running writes the creation rule and
#      installs the key, with no token.
#   2. `secret_set` creates an encrypted file (no plaintext in the tree), a concept
#      declares it in secret_refs, `secret_resolve` redacts and `reveal` returns the value.
#   3. With the key removed the resolution is refused and says how to fix it.
#   4. With a key that is not a recipient the resolution fails and the value never appears.
#
# Optional: not in `make e2e`. Needs `sops` and `age-keygen` on PATH.
# TRAP: the client of this machine may point at a real server (D177); every command here
# runs with HOME and the XDG variables under the scenario's directory, and `kb secrets init`
# is given --local and --data so it cannot reach another server.
#
# Expected environment variables: E2E_TMP_DIR, E2E_HTTP_PORT, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# shellcheck source=../lib/assert.sh
source "${E2E_DIR}/lib/assert.sh"
# shellcheck source=../lib/server.sh
source "${E2E_DIR}/lib/server.sh"

SCENARIO_NAME="32_secrets_lifecycle"
echo "=== Scenario ${SCENARIO_NAME} ==="

for tool in sops age-keygen; do
    command -v "$tool" >/dev/null 2>&1 || { echo "[ERROR] ${SCENARIO_NAME} needs ${tool} on PATH" >&2; exit 1; }
done

BIN="${REPO_ROOT}/bin/cartographer"
DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
DATA="${DIR}/data"
CRED="${DIR}/credentials"
CONFIG="${DIR}/config.yaml"
KB="${DATA}/kb-a"
TOKEN="sec-token"
VALUE="s3cr3t-value-e2e"
mkdir -p "$DATA" "${DIR}/home"
export HOME="${DIR}/home" XDG_CONFIG_HOME="${DIR}/home/.config" XDG_CACHE_HOME="${DIR}/home/.cache" \
    XDG_DATA_HOME="${DIR}/home/.local/share" XDG_STATE_HOME="${DIR}/home/.local/state"

age-keygen -o "${DIR}/good.age" >/dev/null 2>&1
age-keygen -o "${DIR}/other.age" >/dev/null 2>&1
PUB="$(age-keygen -y "${DIR}/good.age")"

"$BIN" kb create kb-a --no-remote --local --data "$DATA" >/dev/null 2>&1 || { echo "[ERROR] kb create failed"; exit 1; }
# An explicit http: on a free port: without it `kb secrets init` probes the default address, where a
# real server of this machine may answer and take the rule into its own KB.
INIT_PORT="$(e2e_free_port)" || exit 1
printf 'http: "127.0.0.1:%s"\ndata: "%s"\ncredentials_dir: "%s"\n' "$INIT_PORT" "$DATA" "$CRED" > "$CONFIG"

echo ""
echo "--- Phase 1: kb secrets init with no server running ---"
OUT="$("$BIN" kb secrets init kb-a --recipient "$PUB" --key-file "${DIR}/good.age" --config "$CONFIG" --local --data "$DATA" 2>&1)"
assert_mcp_contains "the rule is written" "$OUT" "rule: created"
assert_mcp_contains "the key is installed" "$OUT" "key: installed"
assert_file_exists "${CRED}/kb-a.age"
assert_file_contains "${KB}/.sops.yaml" "$PUB"

CARTOGRAPHER_AUTH=true CARTOGRAPHER_TOKENS="$TOKEN" CARTOGRAPHER_DATA="$DATA" CARTOGRAPHER_CREDENTIALS_DIR="$CRED" CARTOGRAPHER_NO_UPDATE_CHECK=1 CARTOGRAPHER_AUDIT_LOG="${DIR}/audit/audit.log" \
    e2e_serve "${DIR}/server.log" --config "$CONFIG" || { echo "[ERROR] server not responding"; exit 1; }
SERVER_PID="$E2E_SERVE_PID"
trap 'kill "$SERVER_PID" 2>/dev/null || true' EXIT
URL="http://127.0.0.1:${E2E_SERVE_PORT}/mcp?kb=kb-a"
call() { mcp_call "${URL%%\?*}" kb-a "$TOKEN" "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$1\",\"arguments\":$2}}"; }

echo ""
echo "--- Phase 2: a secret set, declared, redacted and revealed ---"
OUT="$(call secret_set "{\"path\":\"secrets/svc.sops.yaml\",\"key\":\"/token\",\"value\":\"${VALUE}\"}")"
assert_mcp_ok "secret_set creates the encrypted file" "$OUT"
assert_file_exists "${KB}/secrets/svc.sops.yaml"
if grep -rqF --include='*.yaml' --include='*.md' -e "$VALUE" "$KB"; then _assert_fail "the plaintext value is in the KB tree"; else _assert_pass "no plaintext value in the KB tree"; fi
OUT="$(call concept_write '{"id":"services/svc","frontmatter":{"type":"service","title":"Svc","secret_refs":["TOKEN=secrets/svc.sops.yaml#/token"]},"body":"x"}')"
assert_mcp_ok "a concept declares the secret" "$OUT"
OUT="$(call secret_resolve '{"concept_id":"services/svc"}')"
assert_mcp_contains "without reveal the value is redacted" "$OUT" "TOKEN=\\u003credacted\\u003e"
assert_mcp_not_discloses "without reveal the value is absent" "$OUT" "$VALUE"
OUT="$(call secret_resolve '{"concept_id":"services/svc","reveal":true}')"
assert_mcp_contains "with reveal the value is returned" "$OUT" "TOKEN=${VALUE}"

echo ""
echo "--- Phase 3: the key is missing ---"
mv "${CRED}/kb-a.age" "${DIR}/kb-a.age.away"
OUT="$(call secret_resolve '{"concept_id":"services/svc","reveal":true}')"
assert_mcp_contains "a missing key is refused" "$OUT" '"isError":true'
assert_mcp_contains "the refusal names the key" "$OUT" "age key"
assert_mcp_not_discloses "the value is not returned" "$OUT" "$VALUE"

echo ""
echo "--- Phase 4: the key is not a recipient ---"
cp "${DIR}/other.age" "${CRED}/kb-a.age"
chmod 600 "${CRED}/kb-a.age"
OUT="$(call secret_resolve '{"concept_id":"services/svc","reveal":true}')"
assert_mcp_contains "a wrong key is refused" "$OUT" '"isError":true'
assert_mcp_not_discloses "the value is not returned" "$OUT" "$VALUE"
cp "${DIR}/good.age" "${CRED}/kb-a.age"
OUT="$(call secret_resolve '{"concept_id":"services/svc","reveal":true}')"
assert_mcp_contains "the right key works again" "$OUT" "TOKEN=${VALUE}"

if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
