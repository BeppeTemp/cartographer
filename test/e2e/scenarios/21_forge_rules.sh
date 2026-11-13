#!/usr/bin/env bash
# scenarios/21_forge_rules.sh — OPERATOR scenario: a remote that enforces a
# membership rule (D996).
#
# The real server on a KB whose bare remote carries test/forge/pre-receive.sh
# with an allow-list (what GitLab's member check does: the author AND the
# committer of every pushed commit must be a member), on a host with no git
# identity (run.sh isolates the git config):
#   a. a write that names its author in the Cartographer-Author header lands,
#      and the remote head has that author and committer;
#   b. a write with no header, on a local server with no git identity, is refused
#      before it is committed (D1974): the tool result names the
#      key to set, nothing is committed and the remote does not move;
#   b2. an explicit empty git.identities entry keeps the server identity (D1725): the
#      write is committed as local-admin and the forge refuses the push, the tool
#      result says so (neutral wording, D1101) and the remote does not move;
#   c. that local-admin commit is listed by the `kb reauthor` preview, which needs no identity; git.identities.default
#      of the allowed user is set; `kb reauthor` re-authors it, and after a restart a
#      header-less write publishes with it;
#   d. a commit made with no writer (server identity) is re-committed by that
#      identity and publishes with the next write.
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

SCENARIO_NAME="21_forge_rules"
echo "=== Scenario ${SCENARIO_NAME} ==="

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
BARE="${DIR}/remote.git"
KB_DIR="${DIR}/kb-rules"
CONFIG="${DIR}/config.yaml"
BIN="${REPO_ROOT}/bin/cartographer"
MCP="http://127.0.0.1:${E2E_HTTP_PORT}/mcp?kb=kb-rules"
ADA='Ada Example <ada@example.test>'
mkdir -p "$DIR"

# A push is made before the write returns (as on Cloud Run), so the tool result
# carries the forge's answer.
export K_SERVICE=e2e

# call TOOL ARGS [AUTHOR-HEADER] prints the raw JSON-RPC response.
call() {
    local header=()
    [[ -n "${3:-}" ]] && header=(-H "Cartographer-Author: $3")
    curl -s -X POST "$MCP" ${header[@]+"${header[@]}"} -H 'content-type: application/json' \
        -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$1\",\"arguments\":$2}}"
}
# page NAME BODY [AUTHOR-HEADER] writes an Index page and the note it links, in
# one concept_batch: the write gate (D907) refuses an orphan or an unlisted page.
page() {
    call concept_batch "{\"operations\":[{\"op\":\"write\",\"id\":\"rules/$1\",\"index_entry\":true,\"frontmatter\":{\"type\":\"Index\",\"title\":\"$1\"},\"body\":\"# $1\\n\\nSee [[rules/$1/note]].\\n\"},{\"op\":\"write\",\"id\":\"rules/$1/note\",\"index_entry\":true,\"frontmatter\":{\"type\":\"note\",\"title\":\"$1 note\"},\"body\":\"$2\"}]}" "${3:-}"
}
remote_head() { git -C "$BARE" log -1 --format="$1" main; }

# --- The remote and the KB, seeded before the forge turns its rule on ---
git init --bare "$BARE" >/dev/null 2>&1
git -C "$BARE" symbolic-ref HEAD refs/heads/main
kb_bootstrap "$KB_DIR"
git -C "$KB_DIR" remote add origin "$BARE"
git -C "$KB_DIR" push -u origin "$(git -C "$KB_DIR" branch --show-current)" >/dev/null 2>&1

# The server's own commit at its first start (settings realign) is by the server
# identity: the operator publishes it before the forge turns the rule on.
printf 'http: "127.0.0.1:%s"\n' "$E2E_HTTP_PORT" > "$CONFIG"
E2E_CONFIG="$CONFIG" server_start "$KB_DIR"
server_wait_health 20
trap 'server_stop' EXIT
git -C "$KB_DIR" push origin main >/dev/null 2>&1

cp "${REPO_ROOT}/test/forge/pre-receive.sh" "${BARE}/hooks/pre-receive"
chmod +x "${BARE}/hooks/pre-receive"
printf '# members\nada@example.test\n' > "${BARE}/hooks/allowed-identities"
SEED="$(remote_head %H)"

echo ""
echo "--- a. A write that names its author lands ---"
call map_create '{"name":"rules","title":"Rules"}' "$ADA" >/dev/null
OUT="$(page one "Written by a member." "$ADA")"
if grep -q -- 'sync_state' <<<"$OUT"; then _assert_fail "the push of a member's write failed: ${OUT}"; else _assert_pass "no sync warning on a member's write"; fi
[[ "$(remote_head '%an <%ae> | %cn <%ce>')" == "Ada Example <ada@example.test> | Ada Example <ada@example.test>" ]] \
    && _assert_pass "the remote head is by the header's author and committer" \
    || _assert_fail "remote head = $(remote_head '%an <%ae> | %cn <%ce>')"
HEAD_A="$(remote_head %H)"

echo ""
echo "--- b. A write with no header and no git identity is refused before it is committed ---"
OUT="$(page two "No author.")"
if grep -q -- 'git.identities.default' <<<"$OUT" && grep -q -- 'user.email' <<<"$OUT"; then
    _assert_pass "the tool result names the keys to set"
else
    _assert_fail "the tool result does not report the refusal: ${OUT}"
fi
[[ "$(remote_head %H)" == "$HEAD_A" ]] && _assert_pass "the remote did not move" || _assert_fail "the remote moved"
[[ ! -f "${KB_DIR}/data/rules/two/note.md" ]] && _assert_pass "nothing was written to the local clone" || _assert_fail "the refused write reached the clone"
[[ "$(git -C "$KB_DIR" rev-list --count "origin/main..HEAD")" -eq 0 ]] && _assert_pass "no local-only commit" || _assert_fail "a local-only commit exists"

echo ""
echo "--- b2. An explicit empty git.identities entry keeps the server identity: the forge refuses the push ---"
# "none" on purpose (D1725) exempts the KB from the requirement: the write is committed
# as local-admin and the forge's author rule rejects the push.
server_stop
printf 'http: "127.0.0.1:%s"\ngit:\n  identities:\n    default: ""\n' "$E2E_HTTP_PORT" > "$CONFIG"
E2E_CONFIG="$CONFIG" server_start "$KB_DIR"
server_wait_health 20
OUT="$(page two "No author.")"
if grep -q -- 'is not a member of team' <<<"$OUT" && grep -q -- 'failed' <<<"$OUT"; then
    _assert_pass "the tool result reports the refused push with the forge's message"
else
    _assert_fail "the tool result does not report the refusal: ${OUT}"
fi
# A generic connection gets the neutral recovery, with no CLI command (D1101).
if grep -q -- 'local-admin' <<<"$OUT" && grep -q -- "Tell the server's operator" <<<"$OUT" \
    && ! grep -q -- 'cartographer kb' <<<"$OUT" && ! grep -qi -- 'unprotect' <<<"$OUT"; then
    _assert_pass "the refusal names the identity and the recovery, and does not blame a protected branch"
else
    _assert_fail "the refusal is not worded as an identity refusal: ${OUT}"
fi
[[ "$(remote_head %H)" == "$HEAD_A" ]] && _assert_pass "the remote did not move" || _assert_fail "the remote moved"
[[ "$(git -C "$KB_DIR" rev-list --count "origin/main..HEAD")" -ge 1 ]] && _assert_pass "the commit is local only" || _assert_fail "no local-only commit"

echo ""
echo "--- c. git.identities.default of the allowed user lets a header-less write publish ---"
server_stop
REFUSED="$(git -C "$KB_DIR" log -1 --format='%an|%cn|%ae|%ce')"
[[ "$REFUSED" == "local-admin|local-admin|"* && "$REFUSED" != *ada@example.test* ]] \
    && _assert_pass "the local commit is by the technical identity, author and committer" \
    || _assert_fail "local commit identity = ${REFUSED}"
[[ "$(remote_head %H)" == "$HEAD_A" ]] && _assert_pass "the remote head is still the last member's commit" || _assert_fail "the remote head moved"
printf 'http: "127.0.0.1:%s"\n' "$E2E_HTTP_PORT" > "$CONFIG"
NOID="$("${BIN}" kb reauthor kb-rules --data "$DIR" --config "$CONFIG" 2>&1)"
if grep -q -- 'local-admin' <<<"$NOID" && grep -q -- 'git.identities.default' <<<"$NOID"; then
    _assert_pass "the preview lists the commit without any identity and names the key to set"
else
    _assert_fail "preview without identity = ${NOID}"
fi
printf 'http: "127.0.0.1:%s"\ngit:\n  identities:\n    default: "%s"\n' "$E2E_HTTP_PORT" "$ADA" > "$CONFIG"
REAUTHOR=("${BIN}" kb reauthor kb-rules --data "$DIR" --config "$CONFIG")
PREVIEW="$("${REAUTHOR[@]}" 2>&1)"
if grep -q -- 'local-admin' <<<"$PREVIEW" && grep -q -- 'would be re-authored' <<<"$PREVIEW"; then
    _assert_pass "the preview lists the token-authored commit"
else
    _assert_fail "preview = ${PREVIEW}"
fi
[[ "$(remote_head %H)" == "$HEAD_A" ]] && _assert_pass "the preview did not push" || _assert_fail "the preview pushed"
if "${REAUTHOR[@]}" --apply >"${DIR}/reauthor.log" 2>&1; then _assert_pass "kb reauthor --apply succeeds"; else _assert_fail "kb reauthor --apply failed: $(cat "${DIR}/reauthor.log")"; fi
[[ "$(remote_head '%an <%ae> | %cn <%ce>')" == "Ada Example <ada@example.test> | Ada Example <ada@example.test>" ]] \
    && _assert_pass "the refused commit is published, by the configured identity" \
    || _assert_fail "remote head = $(remote_head '%an <%ae> | %cn <%ce>')"
E2E_CONFIG="$CONFIG" server_start "$KB_DIR"
server_wait_health 20
OUT="$(page three "Identity from the server file.")"
if grep -q -- 'sync_state' <<<"$OUT"; then _assert_fail "the push failed with git.identities.default set: ${OUT}"; else _assert_pass "no sync warning once git.identities.default is set"; fi
[[ "$(remote_head '%an <%ae> | %cn <%ce>')" == "Ada Example <ada@example.test> | Ada Example <ada@example.test>" ]] \
    && _assert_pass "the remote head is by the configured identity" \
    || _assert_fail "remote head = $(remote_head '%an <%ae> | %cn <%ce>')"

echo ""
echo "--- d. A commit made with no writer is re-committed by git.identity and publishes ---"
# What the server commits unattended (a repair, a merge) is by the server
# identity; the rule would refuse it, so the pull replays it as the configured
# identity (D950). Made here by hand, with the server's name, while it is stopped.
server_stop
echo "unattended" > "${KB_DIR}/data/rules/unattended.md"
git -C "$KB_DIR" add -A
git -C "$KB_DIR" -c user.name=Cartographer -c user.email=cartographer@example.test commit -q -m "repair: unattended"
E2E_CONFIG="$CONFIG" server_start "$KB_DIR"
server_wait_health 20
OUT="$(page four "After the unattended commit.")"
if grep -q -- 'sync_state' <<<"$OUT"; then _assert_fail "the push failed with an unattended commit pending: ${OUT}"; else _assert_pass "no sync warning with an unattended commit pending"; fi
git -C "$BARE" show main:data/rules/unattended.md >/dev/null 2>&1 && _assert_pass "the unattended commit reached the remote" || _assert_fail "the unattended commit did not reach the remote"
BAD="$(git -C "$BARE" log "${SEED}..main" --format='%ae %ce' | grep -vc '^ada@example.test ada@example.test$')"
[[ "$BAD" == "0" ]] && _assert_pass "every published commit has the member as author and committer" || _assert_fail "${BAD} published commit(s) by someone else"

echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
