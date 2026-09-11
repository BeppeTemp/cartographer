#!/usr/bin/env bash
# scenarios/18_workspace_projection.sh — OPERATOR scenario: workspace-scoped
# artifact projection (D193). This is the plan's mandatory acceptance criterion,
# and it is a scenario that CANNOT pass before the scope exists.
#
# The originating incident: a session working in the HomeLab perimeter activated
# a skill belonging to the DANTE perimeter, because the provider chooses a skill
# from name and description alone and there was one global catalogue.
#
# Setup: two KBs holding a skill with the SAME NAME, two workspaces, one
# provider bound to a different KB in each.
#
# Verifies (operator channel only — no agent/model):
#   1. Each workspace receives only its own KB's skill, and the bodies differ:
#      the projection reached the right perimeter, not merely *a* file.
#   2. Neither skill exists in the global catalogue under $HOME — decision 5.
#   3. Cartographer's transversal bundled skills DO stay global — decision 4.
#   4. The two workspaces are independent: unbinding one prunes only its files.
#   5. Generated files do not dirty the repository: .git/info/exclude carries
#      Cartographer's block, .gitignore is untouched, `git status` is clean.
#   6. An unbound workspace is fail-closed: it receives no KB artifact at all.
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

SCENARIO_NAME="18_workspace_projection"

echo "=== Scenario ${SCENARIO_NAME} ==="

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
CONFIG="${DIR}/config.yaml"
SANDBOX="${DIR}/home"
BIN="${REPO_ROOT}/bin/cartographer"
SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"

DANTE_KB="${DIR}/dante-kb"
HOMELAB_KB="${DIR}/homelab-kb"
WS_DANTE="${DIR}/work/dante-app"
WS_HOMELAB="${DIR}/work/homelab-infra"
WS_UNBOUND="${DIR}/work/scratch"

mkdir -p "$DIR" "$SANDBOX" "$WS_DANTE" "$WS_HOMELAB" "$WS_UNBOUND"
kb_make "$DANTE_KB"
kb_make "$HOMELAB_KB"

# The same skill NAME in both KBs, with bodies that name their perimeter. A
# projection that reaches the wrong KB produces a readable file, so asserting
# existence alone would pass on the very bug this scenario exists to catch.
write_skill() {
    local kb="$1" marker="$2"
    mkdir -p "${kb}/skills/new-microservice"
    cat > "${kb}/skills/new-microservice/SKILL.md" <<SKILL
---
name: new-microservice
description: Scaffold a new microservice in this perimeter.
---
# New microservice

PERIMETER-${marker}
SKILL
}
write_skill "$DANTE_KB" "DANTE"
write_skill "$HOMELAB_KB" "HOMELAB"

# Each workspace is a git repository: rule 5 is about not dirtying one.
for ws in "$WS_DANTE" "$WS_HOMELAB" "$WS_UNBOUND"; do
    git -C "$ws" init -q
    git -C "$ws" config user.email e2e@example.com
    git -C "$ws" config user.name E2E
    printf 'node_modules/\n' > "${ws}/.gitignore"
    printf '# project\n' > "${ws}/README.md"
    git -C "$ws" add -A
    git -C "$ws" commit -q -m "initial"
done

cat > "$CONFIG" <<YAML
http: ":${E2E_HTTP_PORT}"
init: true
mcp:
  tool_prefix_mode: "off"
kbs:
  - path: ${DANTE_KB}
    name: dante-kb
  - path: ${HOMELAB_KB}
    name: homelab-kb
YAML

E2E_CONFIG="$CONFIG" server_start "${DANTE_KB},${HOMELAB_KB}"
server_wait_health 20
trap 'server_stop' EXIT

run_client() {
    local out="$1"; shift
    (cd "$SANDBOX" && HOME="$SANDBOX" "$BIN" "$@") >"$out" 2>&1
    return $?
}

echo ""
echo "--- Phase 1: connect, then bind each workspace to its own KB ---"

# Bound to ONE KB globally: that is the pre-D193 state this plan moves away
# from, and it is also the only global state these two KBs can be in — binding
# a single catalogue to both is refused as a collision (D171), which is phase 7.
run_client "${DIR}/connect.log" connect claude --server-url "$SERVER_URL" --kb dante-kb --auto-trust || true
assert_file_exists "${SANDBOX}/.claude.json"

# The global catalogue holds the DANTE skill, reachable from every directory on
# the machine — the exposure the workspace scope exists to close.
assert_file_exists "${SANDBOX}/.claude/skills/new-microservice/SKILL.md"
assert_file_contains "${SANDBOX}/.claude/skills/new-microservice/SKILL.md" "PERIMETER-DANTE"

if run_client "${DIR}/bind-dante.log" workspace bind claude "$WS_DANTE" --kb dante-kb; then
    _assert_pass "workspace bind claude <dante workspace> --kb dante-kb"
else
    _assert_fail "workspace bind failed: $(cat "${DIR}/bind-dante.log")"
fi
assert_file_contains "${DIR}/bind-dante.log" "workspace scope"

if run_client "${DIR}/bind-homelab.log" workspace bind claude "$WS_HOMELAB" --kb homelab-kb; then
    _assert_pass "workspace bind claude <homelab workspace> --kb homelab-kb"
else
    _assert_fail "workspace bind failed: $(cat "${DIR}/bind-homelab.log")"
fi

if run_client "${DIR}/sync1.log" sync; then
    _assert_pass "sync projects both workspaces"
else
    _assert_fail "sync failed: $(cat "${DIR}/sync1.log")"
fi

echo ""
echo "--- Phase 2: each workspace sees ONLY its own perimeter ---"

DANTE_SKILL="${WS_DANTE}/.claude/skills/new-microservice/SKILL.md"
HOMELAB_SKILL="${WS_HOMELAB}/.claude/skills/new-microservice/SKILL.md"

assert_file_exists "$DANTE_SKILL"
assert_file_exists "$HOMELAB_SKILL"
assert_file_contains "$DANTE_SKILL" "PERIMETER-DANTE"
assert_file_not_contains "$DANTE_SKILL" "PERIMETER-HOMELAB"
assert_file_contains "$HOMELAB_SKILL" "PERIMETER-HOMELAB"
assert_file_not_contains "$HOMELAB_SKILL" "PERIMETER-DANTE"

echo ""
echo "--- Phase 3: no KB artifact is left in the global catalogue ---"

# Decision 5: nothing KB-sourced is materialized globally under workspace scope.
assert_file_not_exists "${SANDBOX}/.claude/skills/new-microservice/SKILL.md"

# Decision 4: Cartographer's own transversal bundled skills DO stay global —
# they belong to no perimeter, and taking them away would break every session
# that is not inside a bound workspace.
assert_file_exists "${SANDBOX}/.claude/skills/cartographer-ops/SKILL.md"
assert_file_not_exists "${WS_DANTE}/.claude/skills/cartographer-ops/SKILL.md"

echo ""
echo "--- Phase 4: an unbound workspace is fail-closed ---"

if [[ -d "${WS_UNBOUND}/.claude/skills" ]]; then
    _assert_fail "an unbound workspace received artifacts: $(ls "${WS_UNBOUND}/.claude/skills")"
else
    _assert_pass "an unbound workspace receives no KB artifact"
fi

echo ""
echo "--- Phase 5: the repository is not dirtied ---"

for ws in "$WS_DANTE" "$WS_HOMELAB"; do
    EXCLUDE="${ws}/.git/info/exclude"
    assert_file_exists "$EXCLUDE"
    assert_file_contains "$EXCLUDE" "cartographer (managed)"
    assert_file_contains "$EXCLUDE" "/.claude/skills"
    # .gitignore is the repository's, shared with everyone who clones it.
    assert_file_contains "${ws}/.gitignore" "node_modules/"
    assert_file_not_contains "${ws}/.gitignore" "cartographer"

    STATUS="$(git -C "$ws" status --porcelain)"
    if [[ -z "$STATUS" ]]; then
        _assert_pass "$(basename "$ws"): git status is clean after the projection"
    else
        _assert_fail "$(basename "$ws"): the projection dirtied the repository:
${STATUS}"
    fi
done

echo ""
echo "--- Phase 6: the workspaces are independent ---"

# The bindings persist the CANONICAL path (symlinks resolved once, at bind
# time), which on macOS turns /var into /private/var. Compare against that.
CANON_DANTE="$(cd "$WS_DANTE" && pwd -P)"
CANON_HOMELAB="$(cd "$WS_HOMELAB" && pwd -P)"

run_client "${DIR}/list.log" workspace list
assert_file_contains "${DIR}/list.log" "$CANON_DANTE"
assert_file_contains "${DIR}/list.log" "$CANON_HOMELAB"
assert_file_contains "${DIR}/list.log" "dante-kb"
assert_file_contains "${DIR}/list.log" "homelab-kb"

run_client "${DIR}/status.log" status || true
assert_file_contains "${DIR}/status.log" "workspace ${CANON_DANTE}"

# Unbinding one workspace prunes only its files.
if run_client "${DIR}/unbind.log" workspace unbind claude "$WS_DANTE"; then
    _assert_pass "workspace unbind claude <dante workspace>"
else
    _assert_fail "workspace unbind failed: $(cat "${DIR}/unbind.log")"
fi
if run_client "${DIR}/sync2.log" sync; then
    _assert_pass "sync after unbind"
else
    _assert_fail "sync after unbind failed: $(cat "${DIR}/sync2.log")"
fi

assert_file_not_exists "$DANTE_SKILL"
assert_file_exists "$HOMELAB_SKILL"
assert_file_contains "$HOMELAB_SKILL" "PERIMETER-HOMELAB"

echo ""
echo "--- Phase 7: a collision is still refused where it can actually confuse ---"

# Decision 6: the same two KBs are legal in two workspaces, and NOT legal in
# one. The check is scoped to the projection, not weakened.
run_client "${DIR}/bind-both.log" workspace bind claude "$WS_UNBOUND" --kb dante-kb,homelab-kb
if run_client "${DIR}/sync3.log" sync; then
    _assert_fail "one workspace bound to two KBs with a same-named skill synced without complaint"
else
    _assert_pass "one workspace bound to both KBs is refused as a collision"
fi
assert_file_contains "${DIR}/sync3.log" "new-microservice"

# The refusal protected the configuration: the other workspace is untouched.
assert_file_exists "$HOMELAB_SKILL"

echo ""
if [[ "${E2E_FAILURES}" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
