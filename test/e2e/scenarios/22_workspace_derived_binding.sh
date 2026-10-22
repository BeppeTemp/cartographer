#!/usr/bin/env bash
# scenarios/22_workspace_derived_binding.sh — OPERATOR scenario: a repository a
# KB declares in its paths.yaml is bound to that KB (D1019).
#
# Setup: one KB whose paths.yaml declares a repository it works on (and one it
# only cites, `bind: false`), both cloned locally; one provider in workspace
# scope through an unrelated explicit binding. No `workspace bind` runs in the
# declared repository.
#
# Verifies (operator channel only — no agent/model):
#   1. `sync` projects the KB's skill into the declared repository, and the
#      repository's `git status` stays clean.
#   2. The repository that is only cited (`bind: false`) receives nothing.
#   3. `workspace list` shows the binding with its source.
#   4. The bridge started in the repository serves the KB with write tools.
#   5. Where the repository tracks CLAUDE.md, the instructions block goes to the
#      untracked CLAUDE.local.md and the tracked file is not touched.
#   6. An explicit `--no-kb` binding on the same path replaces the derived one,
#      and the next sync prunes the projection.
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

SCENARIO_NAME="22_workspace_derived_binding"

echo "=== Scenario ${SCENARIO_NAME} ==="

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
CONFIG="${DIR}/config.yaml"
SANDBOX="${DIR}/home"
BIN="${REPO_ROOT}/bin/cartographer"
SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"

KB="${DIR}/kb-a"
REPO_WORKED="${DIR}/code/worked-on"
REPO_CITED="${DIR}/code/cited"
ELSEWHERE="${DIR}/code/elsewhere"

mkdir -p "$DIR" "$SANDBOX" "$REPO_WORKED" "$REPO_CITED" "$ELSEWHERE"
kb_make "$KB"

mkdir -p "${KB}/skills/team-skill"
cat > "${KB}/skills/team-skill/SKILL.md" <<'SKILL'
---
name: team-skill
description: A skill of kb-a.
---
# Team skill

PERIMETER-KB-A
SKILL

cat > "${KB}/paths.yaml" <<'YAML'
repos:
  worked-on:
    description: The repository kb-a works on
    remote: example.com/owner/worked-on
  cited:
    description: A repository kb-a only cites
    remote: example.com/owner/cited
    bind: false
YAML

# Each repository is a clone of its declared remote. The worked-on one tracks a
# CLAUDE.md: a derived binding must not write into it.
git_init() {
    local ws="$1" remote="$2"
    git -C "$ws" init -q
    git -C "$ws" config user.email e2e@example.com
    git -C "$ws" config user.name E2E
    git -C "$ws" remote add origin "https://${remote}.git"
    printf '# project\n' > "${ws}/README.md"
}
git_init "$REPO_WORKED" example.com/owner/worked-on
git_init "$REPO_CITED" example.com/owner/cited
git_init "$ELSEWHERE" example.com/owner/elsewhere
printf '# Team rules\n' > "${REPO_WORKED}/CLAUDE.md"
for ws in "$REPO_WORKED" "$REPO_CITED" "$ELSEWHERE"; do
    git -C "$ws" add -A
    git -C "$ws" commit -q -m "initial"
done

cat > "$CONFIG" <<YAML
http: "127.0.0.1:${E2E_HTTP_PORT}"
YAML

E2E_CONFIG="$CONFIG" server_start "$KB"
server_wait_health 20
trap 'server_stop' EXIT

run_client() {
    local out="$1"; shift
    (cd "$SANDBOX" && HOME="$SANDBOX" "$BIN" "$@") >"$out" 2>&1
    return $?
}

echo ""
echo "--- Phase 1: the provider is in workspace scope, nothing is bound in the repositories ---"

run_client "${DIR}/connect.log" connect claude --server-url "$SERVER_URL" --kb kb-a || true
run_client "${DIR}/bind-elsewhere.log" workspace bind claude "$ELSEWHERE" --no-kb || true
run_client "${DIR}/paths-worked.log" paths set worked-on "$REPO_WORKED" || true
run_client "${DIR}/paths-cited.log" paths set cited "$REPO_CITED" || true

if run_client "${DIR}/sync1.log" sync; then
    _assert_pass "sync succeeds"
else
    _assert_fail "sync failed: $(cat "${DIR}/sync1.log")"
fi

echo ""
echo "--- Phase 2: the declared repository receives the KB, the cited one does not ---"

assert_file_exists "${REPO_WORKED}/.claude/skills/team-skill/SKILL.md"
assert_file_contains "${REPO_WORKED}/.claude/skills/team-skill/SKILL.md" "PERIMETER-KB-A"
assert_file_not_exists "${REPO_CITED}/.claude/skills/team-skill/SKILL.md"

CANON_WORKED="$(cd "$REPO_WORKED" && pwd -P)"
run_client "${DIR}/list.log" workspace list
assert_file_contains "${DIR}/list.log" "$CANON_WORKED"
assert_file_contains "${DIR}/list.log" "derived: paths.yaml of kb-a"

echo ""
echo "--- Phase 3: the repository stays clean, the tracked file is untouched ---"

STATUS="$(git -C "$REPO_WORKED" status --porcelain)"
if [[ -z "$STATUS" ]]; then
    _assert_pass "git status is clean after a derived projection"
else
    _assert_fail "the derived projection dirtied the repository:
${STATUS}"
fi
assert_file_contains "${REPO_WORKED}/CLAUDE.local.md" "cartographer:instructions:begin"
assert_file_not_contains "${REPO_WORKED}/CLAUDE.md" "cartographer"
assert_file_contains "${REPO_WORKED}/.git/info/exclude" "/CLAUDE.local.md"

echo ""
echo "--- Phase 4: the bridge started in the repository serves the KB ---"

TOOLS="${DIR}/bridge-tools.json"
(cd "$REPO_WORKED" && HOME="$SANDBOX" "$BIN" mcp --provider claude <<'MCP'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
MCP
) >"$TOOLS" 2>"${DIR}/bridge-tools.err"
assert_file_contains "$TOOLS" '"name":"concept_read"'
assert_file_contains "$TOOLS" '"name":"concept_write"'

echo ""
echo "--- Phase 5: an explicit binding on the same path wins, and the projection is pruned ---"

run_client "${DIR}/bind-optout.log" workspace bind claude "$REPO_WORKED" --no-kb || true
if run_client "${DIR}/sync2.log" sync; then
    _assert_pass "sync after the local opt-out"
else
    _assert_fail "sync after the opt-out failed: $(cat "${DIR}/sync2.log")"
fi
assert_file_not_exists "${REPO_WORKED}/.claude/skills/team-skill/SKILL.md"
run_client "${DIR}/resolve.log" workspace resolve "$REPO_WORKED" --provider claude
assert_file_not_contains "${DIR}/resolve.log" "derived from"

echo ""
if [[ "${E2E_FAILURES}" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
