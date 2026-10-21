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
#   b. a write with no header is committed by the server identity: the rule
#      refuses it, the tool result says so, and the remote does not move;
#   c. restarted with git.identity of the allowed user, a header-less write is
#      by that user and publishes;
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
if printf '%s' "$OUT" | grep -q 'sync_state'; then _assert_fail "the push of a member's write failed: ${OUT}"; else _assert_pass "no sync warning on a member's write"; fi
[[ "$(remote_head '%an <%ae> | %cn <%ce>')" == "Ada Example <ada@example.test> | Ada Example <ada@example.test>" ]] \
    && _assert_pass "the remote head is by the header's author and committer" \
    || _assert_fail "remote head = $(remote_head '%an <%ae> | %cn <%ce>')"
HEAD_A="$(remote_head %H)"

echo ""
echo "--- b. A write with no header is refused by the rule ---"
OUT="$(page two "No author.")"
if printf '%s' "$OUT" | grep -q 'is not a member of team' && printf '%s' "$OUT" | grep -q 'failed'; then
    _assert_pass "the tool result reports the refused push with the forge's message"
else
    _assert_fail "the tool result does not report the refusal: ${OUT}"
fi
[[ "$(remote_head %H)" == "$HEAD_A" ]] && _assert_pass "the remote did not move" || _assert_fail "the remote moved"
[[ -f "${KB_DIR}/data/rules/two/note.md" ]] && _assert_pass "the write is kept in the local clone" || _assert_fail "the write is missing locally"
[[ "$(git -C "$KB_DIR" rev-list --count "origin/main..HEAD")" -ge 1 ]] && _assert_pass "the commit is local only" || _assert_fail "no local-only commit"

echo ""
echo "--- c. git.identity of the allowed user lets a header-less write publish ---"
# The refused commit is by the token that made it (here local-admin), which the
# server keeps as its author: the operator drops it, as the refusal's hint says
# the change is only safe locally.
server_stop
git -C "$KB_DIR" reset -q --hard origin/main
printf 'http: "127.0.0.1:%s"\ngit:\n  identity: "%s"\n' "$E2E_HTTP_PORT" "$ADA" > "$CONFIG"
E2E_CONFIG="$CONFIG" server_start "$KB_DIR"
server_wait_health 20
OUT="$(page three "Identity from the server file.")"
if printf '%s' "$OUT" | grep -q 'sync_state'; then _assert_fail "the push failed with git.identity set: ${OUT}"; else _assert_pass "no sync warning once git.identity is set"; fi
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
if printf '%s' "$OUT" | grep -q 'sync_state'; then _assert_fail "the push failed with an unattended commit pending: ${OUT}"; else _assert_pass "no sync warning with an unattended commit pending"; fi
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
