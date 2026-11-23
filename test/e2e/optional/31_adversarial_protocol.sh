#!/usr/bin/env bash
# optional/31_adversarial_protocol.sh — the adversarial rows of the acceptance battery
# that a script can judge: malformed and batched JSON-RPC, unknown parameters, negative
# and huge limits, odd ids, a body over the 1 MiB concept limit, a conflicting batch and
# ten parallel writers on one concept.
#
# Optional: not in `make e2e`. Run with `make e2e-nightly` or `--only 31_adversarial_protocol`.
# What it asserts is that the server refuses or accepts each input in a well-formed
# JSON-RPC answer, never a 5xx or a hang, and that the KB is left clean and the server up.
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

SCENARIO_NAME="31_adversarial_protocol"
echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
KB="${DIR}/kb-a"
TOKEN="adv-token"
mkdir -p "$DIR"
kb_make "$KB"
kb_bootstrap "$KB"

mkdir -p "${DIR}/home"
HOME="${DIR}/home" XDG_CONFIG_HOME="${DIR}/home/.config" XDG_CACHE_HOME="${DIR}/home/.cache" CARTOGRAPHER_NO_UPDATE_CHECK=1 \
CARTOGRAPHER_AUTH=true CARTOGRAPHER_TOKENS="$TOKEN" \
CARTOGRAPHER_CREDENTIALS_DIR="${DIR}/credentials" CARTOGRAPHER_AUDIT_LOG="${DIR}/audit/audit.log" \
    e2e_serve "${DIR}/server.log" --kb "$KB" || { echo "[ERROR] server not responding"; exit 1; }
SERVER_PID="$E2E_SERVE_PID"
trap 'kill "$SERVER_PID" 2>/dev/null || true' EXIT
URL="http://127.0.0.1:${E2E_SERVE_PORT}/mcp?kb=kb-a"

# post <body-file-or-json> prints "<http status> <body>"; a body starting with @ is a file.
post() {
    local data="$1"
    curl -s -m 60 -w ' %{http_code}' -X POST "$URL" -H 'Content-Type: application/json' \
        -H "Authorization: Bearer ${TOKEN}" --data-binary "$data"
}
call() { # call <tool> <arguments-json>
    post "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$1\",\"arguments\":$2}}"
}
alive() { [[ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${E2E_SERVE_PORT}/health")" == 200 ]]; }

echo ""
echo "--- Phase 1: malformed, batched and unknown JSON-RPC ---"
OUT="$(post '{not json')"
assert_mcp_contains "malformed JSON is a parse error" "$OUT" '"code":-32700'
OUT="$(post '[{"jsonrpc":"2.0","id":1,"method":"tools/list"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]')"
assert_mcp_contains "a batch answers each request" "$OUT" '"id":2'
assert_mcp_contains "a batch answers the first request" "$OUT" '"id":1'
OUT="$(post '{"jsonrpc":"2.0","id":1,"method":"no/such"}')"
assert_mcp_contains "an unknown method is -32601" "$OUT" '"code":-32601'
OUT="$(call search '{"query":"a","bogus":1}')"
assert_mcp_contains "an unknown parameter is -32602 and names it" "$OUT" 'unknown argument(s) bogus'
OUT="$(call no_such_tool '{}')"
assert_mcp_contains "an unknown tool is an error" "$OUT" '"error"'
alive && _assert_pass "the server is up after Phase 1" || _assert_fail "the server died in Phase 1"

echo ""
echo "--- Phase 2: limits and odd ids ---"
for lim in -5 0 999999999 9223372036854775807; do
    OUT="$(call search "{\"query\":\"a\",\"limit\":${lim}}")"
    if grep -qE ' (200|400)$' <<<"$OUT"; then _assert_pass "search limit ${lim} answers without a server error"
    else _assert_fail "search limit ${lim}: ${OUT:0:300}"; fi
done
for id in '../../etc/passwd' '/abs/path' 'a//b' 'a/./b' '.git/config' 'x%2e%2e/y' "$(printf 'long%.0s' {1..2000})"; do
    OUT="$(call concept_read "{\"id\":\"${id}\"}")"
    if grep -qF '"isError":true' <<<"$OUT"; then _assert_pass "concept_read of an odd id is refused: ${id:0:30}"
    else _assert_fail "concept_read of an odd id was not refused: ${OUT:0:300}"; fi
done
alive && _assert_pass "the server is up after Phase 2" || _assert_fail "the server died in Phase 2"

echo ""
echo "--- Phase 3: frontmatter that is not a mapping, and a body over the limit ---"
OUT="$(call map_create '{"name":"adv","title":"Adversarial"}')"
assert_mcp_ok "a map for the writes" "$OUT"
for fm in '"a string"' '[1,2]' 'null' '{"type":{"nested":"map"}}'; do
    OUT="$(call concept_write "{\"id\":\"adv/odd\",\"frontmatter\":${fm},\"body\":\"x\"}")"
    if grep -qE '"isError":true|"error"' <<<"$OUT"; then _assert_pass "frontmatter ${fm:0:20} is refused"
    else _assert_fail "frontmatter ${fm:0:20} was accepted: ${OUT:0:300}"; fi
done
BIG="${DIR}/big.json"
{
    printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"adv/big","frontmatter":{"type":"note","title":"Big"},"body":"'
    head -c 1300000 /dev/zero | tr '\0' 'x'
    printf '"}}}'
} > "$BIG"
OUT="$(post "@${BIG}")"
assert_mcp_contains "a body over 1 MiB is concept_too_large" "$OUT" 'concept_too_large'
if [[ -e "${KB}/data/adv/big.md" ]]; then _assert_fail "the oversized concept was written"; else _assert_pass "the oversized concept was not written"; fi
alive && _assert_pass "the server is up after Phase 3" || _assert_fail "the server died in Phase 3"

echo ""
echo "--- Phase 4: a conflicting batch, then ten parallel writers on one concept ---"
OUT="$(call concept_batch '{"operations":[{"op":"write","id":"adv/dup","frontmatter":{"type":"note","title":"A"},"body":"a"},{"op":"write","id":"adv/dup","frontmatter":{"type":"note","title":"B"},"body":"b"}]}')"
if grep -qF 'duplicate id in batch' <<<"$OUT"; then _assert_pass "a batch writing one id twice is refused"
else _assert_fail "a batch writing one id twice was accepted: ${OUT:0:300}"; fi
if [[ -e "${KB}/data/adv/dup.md" ]]; then _assert_fail "the refused batch left a file"; else _assert_pass "the refused batch left no file"; fi

OUT="$(call concept_write '{"id":"adv/shared","frontmatter":{"type":"note","title":"Seed"},"body":"seed","index_entry":true}')"
assert_mcp_ok "the shared page is created with its index entry" "$OUT"
OUTDIR="${DIR}/parallel"
mkdir -p "$OUTDIR"
WRITERS=""
for n in 1 2 3 4 5 6 7 8 9 10; do
    call concept_write "{\"id\":\"adv/shared\",\"frontmatter\":{\"type\":\"note\",\"title\":\"Writer ${n}\"},\"body\":\"writer ${n}\"}" > "${OUTDIR}/${n}.out" &
    WRITERS="${WRITERS} $!"
done
# Not a bare `wait`: it would also wait for the server this shell started.
# shellcheck disable=SC2086
wait $WRITERS
BAD=0
for n in 1 2 3 4 5 6 7 8 9 10; do
    grep -qE '"jsonrpc":"2.0".* 200$' "${OUTDIR}/${n}.out" || { BAD=$((BAD + 1)); echo "    writer ${n}: $(cut -c1-200 "${OUTDIR}/${n}.out")"; }
done
if [[ "$BAD" -eq 0 ]]; then _assert_pass "all ten writers got a JSON-RPC answer"; else _assert_fail "${BAD} writer(s) got no JSON-RPC answer"; fi
assert_file_exists "${KB}/data/adv/shared.md"
if [[ "$(grep -c '^title: Writer' "${KB}/data/adv/shared.md")" -eq 1 ]]; then _assert_pass "the page is one writer's, not a mix"; else _assert_fail "the page is not one writer's: $(head -n 8 "${KB}/data/adv/shared.md")"; fi
if [[ -z "$(git -C "$KB" status --porcelain)" ]]; then _assert_pass "the KB has no uncommitted change"; else _assert_fail "uncommitted: $(git -C "$KB" status --porcelain | head -n 5)"; fi
alive && _assert_pass "the server is up after Phase 4" || _assert_fail "the server died in Phase 4"

if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
