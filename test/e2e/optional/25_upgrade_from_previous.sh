#!/usr/bin/env bash
# optional/25_upgrade_from_previous.sh — a client upgraded from the previous
# release converges after one sync (#1173).
#
# Builds the previous stable release from a throwaway git worktree, runs it as
# the client of a server of HEAD until everything the KB serves is installed and
# approved, as a real user's machine is, then switches the client binary to HEAD
# and asserts on what one sync, and a second one, leave behind: no approval
# asked again, no file under a retired name, no dangling local path, no drift.
#
# Optional: not in `make e2e`. Run with `make e2e-upgrade` (release PR, by hand).
#   E2E_UPGRADE_FROM    any ref to upgrade from; default: the newest vX.Y.Z tag
#                       reachable from HEAD that does not point at HEAD
#   E2E_UPGRADE_ONLINE  1: when the module cache lacks a module the old tag
#                       needs, retry the build once with the Go proxy
#
# TRAP: from v0.26.0 on the previous release already has the approval gate
# (D1141), so the one-time pre-approval migration (D1166) is a no-op by default;
# exercise it with E2E_UPGRADE_FROM=v0.25.1. The old binary runs only the
# `connect`, `sync`, `paths set` and `approve` forms every such tag has.
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

SCENARIO_NAME="25_upgrade_from_previous"

echo "=== Scenario ${SCENARIO_NAME} ==="

BIN="${REPO_ROOT}/bin/cartographer"
if [[ ! -x "$BIN" ]]; then
    echo "[ERROR] binary not found: ${BIN}" >&2
    exit 1
fi

WORK_DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
KB_DIR="${WORK_DIR}/kb"
SANDBOX_DIR="${WORK_DIR}/sandbox"
PREV_TREE="${WORK_DIR}/prev"
PREV_BIN="${WORK_DIR}/cartographer-prev"
SERVER_URL="http://127.0.0.1:${E2E_HTTP_PORT}/mcp"
mkdir -p "$WORK_DIR" "$SANDBOX_DIR"

cleanup() {
    server_stop
    if [[ -d "$PREV_TREE" ]]; then
        git -C "$REPO_ROOT" worktree remove --force "$PREV_TREE" >/dev/null 2>&1 || true
    fi
    git -C "$REPO_ROOT" worktree prune >/dev/null 2>&1 || true
}
trap cleanup EXIT

# client <bin> <args…>: one client call in the sandbox's home, stdout+stderr.
client() {
    local bin="$1"
    shift
    (cd "$SANDBOX_DIR" && HOME="$SANDBOX_DIR" CARTOGRAPHER_NO_UPDATE_CHECK=1 "$bin" "$@") 2>&1
}

# snapshot <file>: every file of the sandbox with its checksum, sorted. The
# lockfile is hashed with its managed entries sorted: a sync that rewrote an
# artifact leaves the synthetic hooks' entries to move to the end on the next
# one (ensureSyntheticHook), an order-only rewrite that changes nothing it
# records (#1235: drop the sort once that is fixed).
snapshot() {
    (cd "$SANDBOX_DIR" && find . -type f ! -name .cartographer-sync.lock.json -print0 | sort -z | xargs -0 sha256sum) >"$1"
    jq -S '.providers[].managed |= sort_by(.kind, .name, .path)' "${SANDBOX_DIR}/.cartographer-sync.lock.json" \
        | sha256sum | sed 's|-$|./.cartographer-sync.lock.json (entries sorted)|' >>"$1"
}

# lock_paths <lockfile>: the managed paths a lockfile lists for claude, sorted.
lock_paths() {
    jq -r '.providers.claude.managed[]?.path' "$1" | sort -u
}

# instructions_block <file>: the managed instructions block of a steering file.
instructions_block() {
    sed -n '/<!-- cartographer:instructions:begin/,/<!-- cartographer:instructions:end -->/p' "$1"
}

# --- 1. The previous release ---
if [[ -n "${E2E_UPGRADE_FROM:-}" ]]; then
    PREV_REF="$E2E_UPGRADE_FROM"
else
    # The newest stable tag (no pre-release suffix, as #1192 reads releases)
    # reachable from HEAD; on a release commit, the one before it.
    at_head="$(git -C "$REPO_ROOT" tag --points-at HEAD)"
    PREV_REF="$(git -C "$REPO_ROOT" tag --merged HEAD --list 'v*' --sort=-v:refname \
        | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
        | grep -vxF -f <(printf '%s\n' "$at_head") \
        | head -n 1)"
    if [[ -z "$PREV_REF" ]]; then
        echo "[ERROR] no stable tag reachable from HEAD: a shallow clone or tags not fetched; run: git fetch --tags --unshallow" >&2
        exit 1
    fi
fi
echo "[25] upgrading from ${PREV_REF}"

# --- 2. Its binary, built from a throwaway worktree ---
if ! git -C "$REPO_ROOT" worktree add --detach "$PREV_TREE" "$PREV_REF" >/dev/null 2>&1; then
    echo "[ERROR] cannot check out ${PREV_REF} (a shallow clone? run: git fetch --tags --unshallow)" >&2
    exit 1
fi
build_prev() {
    (cd "$PREV_TREE" && env -u GOFLAGS "$@" go build -ldflags "-X main.version=${PREV_REF}" -o "$PREV_BIN" ./cmd/cartographer) 2>&1
}
if ! build_out="$(build_prev GOPROXY=off)"; then
    if [[ "${E2E_UPGRADE_ONLINE:-}" == "1" ]]; then
        echo "[25] the module cache lacks a module of ${PREV_REF}: retrying with the Go proxy"
        if ! build_out="$(build_prev)"; then
            echo "$build_out" >&2
            echo "[ERROR] cannot build ${PREV_REF}" >&2
            exit 1
        fi
    else
        echo "$build_out" >&2
        echo "[ERROR] cannot build ${PREV_REF} from the module cache; to download what it lacks: E2E_UPGRADE_ONLINE=1" >&2
        exit 1
    fi
fi
echo "[25] built $("$PREV_BIN" --version 2>&1 | head -n 1)"

# What the old tree already has, read from its decision files: whether the
# one-time approval migration ran there (D1166), whether its managed block is
# already brief (D1182).
prev_has() { compgen -G "${PREV_TREE}/docs/decisions/$1-*" >/dev/null; }
PREV_MIGRATED=false
prev_has D1166 && PREV_MIGRATED=true
PREV_BRIEF=false
prev_has D1182 && PREV_BRIEF=true

# --- 3. The KB: a skill, a hook, an MCP descriptor, instructions and a placeholder ---
kb_make "$KB_DIR"
mkdir -p "${KB_DIR}/skills/upgrade-skill" "${KB_DIR}/hooks/upgrade-guard" "${KB_DIR}/mcp"
cat >"${KB_DIR}/skills/upgrade-skill/SKILL.md" <<'EOF'
---
name: upgrade-skill
description: Skill carried across a client upgrade
version: "1.0"
---
# Upgrade skill
Notes live in {{path:upgrade-notes}}.
EOF
printf '%s\n' '{"event":"SessionStart","command":"./run.sh"}' >"${KB_DIR}/hooks/upgrade-guard/hook.json"
printf '#!/bin/sh\nexit 0\n' >"${KB_DIR}/hooks/upgrade-guard/run.sh"
chmod +x "${KB_DIR}/hooks/upgrade-guard/run.sh"
printf '%s\n' '{"type":"http","url":"https://tools.example.com/mcp"}' >"${KB_DIR}/mcp/upgrade-tools.json"
# block: full (D1182) puts this text, placeholder included, in the managed
# block: a brief block would carry neither, and the placeholder is what made
# the block rewrite itself on every sync (#1175).
cat >"${KB_DIR}/instructions.md" <<'EOF'
<!-- cartographer: block: full -->
Keep the upgrade notes in {{path:upgrade-notes}}.
EOF
cat >"${KB_DIR}/paths.yaml" <<'EOF'
paths:
  upgrade-notes: {description: the folder of the upgrade notes, default: ~/upgrade-notes}
EOF
mkdir -p "${SANDBOX_DIR}/upgrade-notes"

server_start "$KB_DIR"
server_wait_health 30 || exit 1

# --- 4. The old client, fully installed ---
echo ""
echo "[25] old client: connect + sync"
if ! client "$PREV_BIN" connect claude --server-url "$SERVER_URL" >"${WORK_DIR}/prev-connect.out"; then
    cat "${WORK_DIR}/prev-connect.out"
    echo "[ERROR] ${PREV_REF} cannot connect to this build's server" >&2
    exit 1
fi
client "$PREV_BIN" paths set path:upgrade-notes "${SANDBOX_DIR}/upgrade-notes" >/dev/null
client "$PREV_BIN" sync >"${WORK_DIR}/prev-sync.out"
cat "${WORK_DIR}/prev-sync.out"
# Approve what waits, as a user does; a tag without the approval gate asks
# nothing, and that is the state D1166 migrates.
mapfile -t waiting < <(sed -n 's/^\[claude\] needs_approval: \([a-z]*\)\/\([^ ]*\) \[kb:kb\]$/\1 \2/p' "${WORK_DIR}/prev-sync.out" | sort -u)
for w in "${waiting[@]}"; do
    read -r kind name <<<"$w"
    client "$PREV_BIN" approve "$kind" "$name" --kb kb --yes >/dev/null
done
if [[ ${#waiting[@]} -gt 0 ]]; then
    client "$PREV_BIN" sync >"${WORK_DIR}/prev-sync2.out"
    if grep -q 'needs_approval' "${WORK_DIR}/prev-sync2.out"; then
        cat "${WORK_DIR}/prev-sync2.out"
        echo "[ERROR] the old client still waits for an approval: the starting state is not fully installed" >&2
        exit 1
    fi
fi
LOCK="${SANDBOX_DIR}/.cartographer-sync.lock.json"
CLAUDE_MD="${SANDBOX_DIR}/.claude/CLAUDE.md"
cp "$LOCK" "${WORK_DIR}/lock.prev"
instructions_block "$CLAUDE_MD" >"${WORK_DIR}/block.prev"

# --- 5. Switch to HEAD: one sync ---
echo ""
echo "[25] HEAD client: sync"
client "$BIN" sync >"${WORK_DIR}/head-sync.out"
head_rc=$?
cat "${WORK_DIR}/head-sync.out"
snapshot "${WORK_DIR}/sandbox.head"
instructions_block "$CLAUDE_MD" >"${WORK_DIR}/block.head"

# --- 6. Assertions ---
echo ""
echo "--- Assertions: the first HEAD sync ---"
if [[ "$head_rc" -eq 0 ]] && grep -q '^synced to revision ' "${WORK_DIR}/head-sync.out"; then
    _assert_pass "HEAD sync exits 0 and reports the synced revision"
else
    _assert_fail "HEAD sync exit ${head_rc}, or no 'synced to revision' line"
fi

if grep -q 'needs_approval' "${WORK_DIR}/head-sync.out"; then
    _assert_fail "HEAD sync asks again for an approval given by ${PREV_REF}: $(grep 'needs_approval' "${WORK_DIR}/head-sync.out" | tr '\n' ' ')"
else
    _assert_pass "no needs_approval for an artifact installed and unchanged"
fi
pending_out="$(client "$BIN" approve --pending --kb kb)"
if grep -q 'nothing is waiting for approval' <<<"$pending_out"; then
    _assert_pass "approve --pending has nothing to approve"
else
    _assert_fail "approve --pending still lists artifacts: ${pending_out}"
fi

migration_line='installed before approvals existed, unchanged'
if [[ "$PREV_MIGRATED" == true ]]; then
    if grep -qF "$migration_line" "${WORK_DIR}/head-sync.out"; then
        _assert_fail "the approval migration ran again although ${PREV_REF} already ran it"
    else
        _assert_pass "no approval migration lines: ${PREV_REF} already ran it (D1166)"
    fi
else
    assert_file_contains "${WORK_DIR}/head-sync.out" "approved hook/upgrade-guard [kb:kb]: ${migration_line}"
    assert_file_contains "${SANDBOX_DIR}/.cartographer.yaml" 'approvals_migrated'
fi

# The approvals recorded by the old client survive the upgrade unchanged:
# mcp_approvals keys the hook as hook/<name>, the descriptor by its name.
assert_file_contains "${SANDBOX_DIR}/.cartographer.yaml" 'hook/upgrade-guard:'
assert_file_contains "${SANDBOX_DIR}/.cartographer.yaml" 'upgrade-tools:'

if grep -E '\] (diverged locally|moved |unsupported)' "${WORK_DIR}/head-sync.out"; then
    _assert_fail "HEAD sync reports a diverged, moved or unsupported artifact"
else
    _assert_pass "no diverged, moved or unsupported artifact"
fi

# No file left under a retired name: the oracle is the two lockfiles, never a
# list of renamed bundled skills, which would rot at the next rename.
lock_paths "${WORK_DIR}/lock.prev" >"${WORK_DIR}/paths.prev"
lock_paths "$LOCK" >"${WORK_DIR}/paths.head"
stray=0
while IFS= read -r f; do
    if ! grep -qxF "$f" "${WORK_DIR}/paths.head"; then
        _assert_fail "file not named by HEAD's lockfile: ${f}"
        stray=1
    fi
done < <(cd "$SANDBOX_DIR" && find .claude/skills .claude/agents .claude/hooks -type f 2>/dev/null | sort)
[[ "$stray" -eq 0 ]] && _assert_pass "every file under the managed directories is in HEAD's lockfile"
retired=0
while IFS= read -r f; do
    if [[ -e "${SANDBOX_DIR}/${f}" ]]; then
        _assert_fail "path dropped from the lockfile still on disk: ${f}"
        retired=1
    fi
done < <(comm -23 "${WORK_DIR}/paths.prev" "${WORK_DIR}/paths.head")
[[ "$retired" -eq 0 ]] && _assert_pass "no path dropped from the lockfile is left on disk ($(comm -23 "${WORK_DIR}/paths.prev" "${WORK_DIR}/paths.head" | wc -l) dropped)"

# No dangling local path: every sandbox path the written files cite exists.
dangling=0
cited=0
while IFS= read -r p; do
    cited=$((cited + 1))
    if [[ ! -e "$p" ]]; then
        _assert_fail "a written file cites a local path that does not exist: ${p}"
        dangling=1
    fi
done < <(cd "$SANDBOX_DIR" && grep -rhoE "${SANDBOX_DIR}/[A-Za-z0-9._/-]*[A-Za-z0-9_-]" .claude 2>/dev/null | sort -u)
if [[ "$cited" -eq 0 ]]; then
    _assert_fail "no written file cites the resolved {{path:upgrade-notes}}: the check would be vacuous"
elif [[ "$dangling" -eq 0 ]]; then
    _assert_pass "every local path cited by a written file exists (${cited} cited)"
fi
assert_file_contains "${SANDBOX_DIR}/.claude/skills/upgrade-skill/SKILL.md" "${SANDBOX_DIR}/upgrade-notes"
assert_file_contains "$CLAUDE_MD" "| \`{{path:upgrade-notes}}\` | \`${SANDBOX_DIR}/upgrade-notes\` |"
# Bundled skills document the placeholder syntax literally, and the block's
# local-paths table names the key beside its path: only the key this KB cites,
# outside that table, must be gone.
unresolved="$(grep -rnF '{{path:upgrade-notes}}' "${SANDBOX_DIR}/.claude" | grep -vF '| `{{path:upgrade-notes}}` | `')"
if [[ -n "$unresolved" ]]; then
    _assert_fail "a written file still carries {{path:upgrade-notes}} unresolved: ${unresolved}"
else
    _assert_pass "{{path:upgrade-notes}} is resolved in every written file"
fi

# The block went brief (D1182): an older client rewrote it whole once.
if [[ "$PREV_BRIEF" == false ]]; then
    if cmp -s "${WORK_DIR}/block.prev" "${WORK_DIR}/block.head"; then
        _assert_fail "the instructions block is unchanged by the first HEAD sync: the brief form of D1182 was not written"
    else
        _assert_pass "the first HEAD sync rewrote the instructions block once (D1182)"
    fi
fi
shared="$(grep -c '^Operational instructions:' "${WORK_DIR}/block.head")"
if [[ "$shared" -eq 1 ]]; then
    _assert_pass "the block carries the shared operational section once"
else
    _assert_fail "the block carries the shared operational section ${shared} times, expected 1"
fi

echo ""
echo "--- Assertions: a second HEAD sync converges ---"
client "$BIN" sync >"${WORK_DIR}/head-sync2.out"
if grep -E '^\[claude\] (wrote|would write|pruned|restored) ' "${WORK_DIR}/head-sync2.out"; then
    _assert_fail "the second HEAD sync wrote files"
else
    _assert_pass "the second HEAD sync writes nothing"
fi
snapshot "${WORK_DIR}/sandbox.head2"
if diff "${WORK_DIR}/sandbox.head" "${WORK_DIR}/sandbox.head2"; then
    _assert_pass "every file of the sandbox is byte-identical across the second sync"
else
    _assert_fail "the second HEAD sync changed files of the sandbox (diff above)"
fi
status_out="$(client "$BIN" status)"
status_rc=$?
if [[ "$status_rc" -eq 0 ]]; then
    _assert_pass "status exits 0 (in sync) after the upgrade"
else
    echo "$status_out"
    _assert_fail "status exits ${status_rc} after the upgrade (expected 0)"
fi
if grep -q 'instructions 1/1' <<<"$status_out"; then
    _assert_pass "status counts the instructions as in sync"
else
    _assert_fail "status does not count the instructions as in sync: ${status_out}"
fi

# --- 7. A revocation survives the upgrade: the D1166 marker is never cleared ---
# A revoked hook stays installed and is reported pending (docs/configurator.md
# §approvals): what must not happen is a sync recording the approval again.
echo ""
echo "--- Assertions: a revoked hook stays revoked ---"
client "$BIN" approve revoke hook upgrade-guard --kb kb >/dev/null
client "$BIN" sync >"${WORK_DIR}/head-sync-revoked.out"
assert_file_contains "${WORK_DIR}/head-sync-revoked.out" 'needs_approval: hook/upgrade-guard [kb:kb]'
assert_file_not_contains "${WORK_DIR}/head-sync-revoked.out" "$migration_line"
assert_file_not_contains "${SANDBOX_DIR}/.cartographer.yaml" 'hook/upgrade-guard:'

# --- Report ---
echo ""
if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS (from ${PREV_REF})"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed, from ${PREV_REF})"
    exit 1
fi
