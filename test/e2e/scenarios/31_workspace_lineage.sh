#!/usr/bin/env bash
# scenarios/31_workspace_lineage.sh — OPERATOR scenario: a binding carries the KBs its
# KBs depend on, and never edits a tracked instructions file for them.
#
# Setup: client-kb declares `depends_on: core-kb` and a repository it works on (paths.yaml);
# a folder is bound explicitly to client-kb and holds that repository (derived) and a second
# repository, bound explicitly too, that tracks an AGENTS.md-style CLAUDE.md. A second provider is bound to client-kb
# at provider scope.
#
# Verifies (operator channel only — no agent/model):
#   1. A derived repository under an explicitly bound folder keeps core-kb.
#   2. A provider bound to client-kb receives core-kb.
#   3. A repository tracking CLAUDE.md never gets core-kb in it while core-kb's skills
#      arrive, and its status says the instructions are not written.
#   4. `--exclude core-kb` opts out: the next sync prunes core-kb's skill.
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

SCENARIO_NAME="31_workspace_lineage"
echo "=== Scenario ${SCENARIO_NAME} ==="

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
SANDBOX="${DIR}/home"
BIN="${REPO_ROOT}/bin/cartographer"
SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"
CLIENT_KB="${DIR}/client-kb"
CORE_KB="${DIR}/core-kb"
FOLDER="${DIR}/code"
REPO_DERIVED="${FOLDER}/worked-on"
REPO_TRACKED="${FOLDER}/tracked"

mkdir -p "$DIR" "$SANDBOX" "$REPO_DERIVED" "$REPO_TRACKED"

make_kb() {
    local dir="$1" name="$2"
    kb_make "$dir"
    mkdir -p "${dir}/skills/skill-${name}"
    printf -- '---\nname: skill-%s\ndescription: A skill of %s.\n---\n# %s\n' "$name" "$name" "$name" > "${dir}/skills/skill-${name}/SKILL.md"
}
make_kb "$CLIENT_KB" client-kb
make_kb "$CORE_KB" core-kb
cat > "${CLIENT_KB}/paths.yaml" <<'YAML'
repos:
  worked-on:
    description: The repository client-kb works on
    remote: example.com/owner/worked-on
YAML
printf 'depends_on:\n  - name: core-kb\n    remote: example.com/owner/core-kb\n' > "${CLIENT_KB}/settings.yaml"

# A KB the server reads relations of needs a remote and a known default branch (kb-relations).
bootstrap_remote() {
    local dir="$1" name="$2"
    kb_bootstrap "$dir"
    git -C "$dir" remote add origin "https://example.com/owner/${name}.git"
    git -C "$dir" add -A
    git -C "$dir" -c user.name=e2e -c user.email=e2e@example.com commit -q -m "fixture" --allow-empty
    git -C "$dir" update-ref refs/remotes/origin/master HEAD
    git -C "$dir" symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/master
}
bootstrap_remote "$CLIENT_KB" client-kb
bootstrap_remote "$CORE_KB" core-kb

git_init() {
    local ws="$1" remote="$2"
    git -C "$ws" init -q
    git -C "$ws" config user.email e2e@example.com
    git -C "$ws" config user.name E2E
    git -C "$ws" remote add origin "https://${remote}.git"
    printf '# project\n' > "${ws}/README.md"
}
git_init "$REPO_DERIVED" example.com/owner/worked-on
git_init "$REPO_TRACKED" example.com/owner/tracked
printf '# Team rules\n' > "${REPO_TRACKED}/CLAUDE.md"
for ws in "$REPO_DERIVED" "$REPO_TRACKED"; do
    git -C "$ws" add -A
    git -C "$ws" commit -q -m initial
done

printf 'http: "127.0.0.1:%s"\n' "$E2E_HTTP_PORT" > "${DIR}/config.yaml"
E2E_CONFIG="${DIR}/config.yaml" server_start "${CLIENT_KB},${CORE_KB}"
server_wait_health 20
trap 'server_stop' EXIT

run_client() {
    local out="$1"; shift
    (cd "$SANDBOX" && HOME="$SANDBOX" "$BIN" "$@") >"$out" 2>&1
}

echo ""
echo "--- Phase 1: a folder bound to client-kb, a provider bound to client-kb ---"
run_client "${DIR}/connect-claude.log" connect claude --server-url "$SERVER_URL" --kb client-kb || true
run_client "${DIR}/connect-codex.log" connect codex --server-url "$SERVER_URL" --kb client-kb || true
run_client "${DIR}/bind.log" workspace bind claude "$FOLDER" --kb client-kb || true
# A projection is written into the bound folder only: the repository that tracks
# CLAUDE.md is bound itself, so the deepest binding is the one under test.
run_client "${DIR}/bind-tracked.log" workspace bind claude "$REPO_TRACKED" --kb client-kb || true
run_client "${DIR}/paths.log" paths set worked-on "$REPO_DERIVED" || true
if run_client "${DIR}/sync1.log" sync; then
    _assert_pass "sync succeeds"
else
    _assert_fail "sync failed: $(cat "${DIR}/sync1.log")"
fi

echo ""
echo "--- Phase 2: the derived repository and the provider keep core-kb ---"
assert_file_contains "${DIR}/sync1.log" "bound by lineage: core-kb (from client-kb)"
assert_file_exists "${REPO_DERIVED}/.claude/skills/skill-core-kb/SKILL.md"
assert_file_exists "${REPO_DERIVED}/.claude/skills/skill-client-kb/SKILL.md"
assert_file_exists "${SANDBOX}/.codex/skills/skill-core-kb/SKILL.md"

echo ""
echo "--- Phase 3: a tracked instructions file is not edited for a lineage KB ---"
assert_file_exists "${REPO_TRACKED}/.claude/skills/skill-core-kb/SKILL.md"
assert_file_not_contains "${REPO_TRACKED}/CLAUDE.md" "core-kb"
run_client "${DIR}/list.log" status
# The repository is bound by hand to client-kb, so its block may enter the tracked
# file; the lineage KB never does, and the status says why its instructions are absent.
assert_file_contains "${REPO_TRACKED}/CLAUDE.md" "# Team rules"
assert_file_contains "${DIR}/list.log" "instructions of core-kb (lineage) not written"

echo ""
echo "--- Phase 4: --exclude opts out ---"
run_client "${DIR}/bind-exclude.log" workspace bind claude "$REPO_TRACKED" --kb client-kb --exclude core-kb || true
if run_client "${DIR}/sync2.log" sync; then
    _assert_pass "sync after the opt-out"
else
    _assert_fail "sync after the opt-out failed: $(cat "${DIR}/sync2.log")"
fi
assert_file_not_exists "${REPO_TRACKED}/.claude/skills/skill-core-kb/SKILL.md"
assert_file_exists "${REPO_TRACKED}/.claude/skills/skill-client-kb/SKILL.md"

echo ""
if [[ "${E2E_FAILURES}" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
