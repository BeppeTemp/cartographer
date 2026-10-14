#!/usr/bin/env bash
# scenarios/19_hero_publish.sh — the README hero's orphan branch (D790).
#
# The hero lives on `readme-assets`, one force-pushed commit, and a release
# re-records it only when its web/ tree differs from the one the hero shows.
# This drives web/scripts/publish-hero.sh and hero-needed.sh against a scratch
# repository and a bare "origin" (no network, no binary).
#
# Verifies:
#   1. With no branch a recording is needed (exit 0).
#   2. After a publish the same tag needs none (exit 1), and a tag whose web/
#      tree differs does (exit 0).
#   3. Publishing twice leaves exactly one commit, with the Web-Tree trailer
#      and the two files, and main's history untouched.
#   4. A tag that changes only files outside web/ needs no recording.
#   5. A label that is not a revision (the bootstrap's `main@<sha>`) takes the
#      tree from HEAD.
#
# Expected environment variables: E2E_TMP_DIR, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# shellcheck source=../lib/assert.sh
source "${E2E_DIR}/lib/assert.sh"

SCENARIO_NAME="19_hero_publish"

echo "=== Scenario ${SCENARIO_NAME} ==="

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
PUBLISH="${REPO_ROOT}/web/scripts/publish-hero.sh"
NEEDED="${REPO_ROOT}/web/scripts/hero-needed.sh"
ORIGIN="${DIR}/origin.git"
WORK="${DIR}/work"

# check <message> <expected exit> <command...>
check() {
    local msg="$1" want="$2" got=0
    shift 2
    "$@" >"${DIR}/out.txt" 2>&1 || got=$?
    if [[ "$got" -eq "$want" ]]; then
        _assert_pass "${msg} (exit ${got})"
    else
        _assert_fail "${msg}: want exit ${want}, got ${got}: $(cat "${DIR}/out.txt")"
    fi
}

mkdir -p "$DIR"
git init -q --bare "$ORIGIN"
git init -q -b main "$WORK"
cd "$WORK" || exit 1
git config user.name e2e
git config user.email e2e@example.com
mkdir web
echo ui-1 > web/app.txt
echo doc-1 > README.md
git add . && git commit -qm "ui 1" && git tag v1
echo ui-2 > web/app.txt
git commit -qam "ui 2" && git tag v2
echo doc-2 > README.md
git commit -qam "docs only" && git tag v3
git remote add origin "$ORIGIN"
head -c 4096 /dev/urandom > hero.webp

echo ""
echo "--- Phase 1: no branch yet ---"
check "a recording is needed" 0 "$NEEDED" v1
assert_file_contains "${DIR}/out.txt" "no readme-assets branch"

echo ""
echo "--- Phase 2: after a publish ---"
check "publish v1" 0 "$PUBLISH" hero.webp v1
check "the same tag needs no recording" 1 "$NEEDED" v1
check "a tag whose web/ differs needs one" 0 "$NEEDED" v2

echo ""
echo "--- Phase 3: one commit, whatever is published ---"
check "publish v2" 0 "$PUBLISH" hero.webp v2
check "publish v2 again" 0 "$PUBLISH" hero.webp v2
COMMITS="$(git -C "$ORIGIN" rev-list --count readme-assets)"
if [[ "$COMMITS" -eq 1 ]]; then
    _assert_pass "readme-assets has exactly one commit"
else
    _assert_fail "readme-assets has ${COMMITS} commits, want 1"
fi
git -C "$ORIGIN" log -1 --format=%B readme-assets > "${DIR}/message.txt"
assert_file_contains "${DIR}/message.txt" "hero: v2"
assert_file_contains "${DIR}/message.txt" "Web-Tree: $(git rev-parse v2:web)"
git -C "$ORIGIN" ls-tree --name-only readme-assets > "${DIR}/tree.txt"
assert_file_contains "${DIR}/tree.txt" "hero.webp"
assert_file_contains "${DIR}/tree.txt" "README.md"
if git -C "$ORIGIN" rev-parse --verify -q main >/dev/null; then
    _assert_fail "the publish pushed to main"
else
    _assert_pass "main was not pushed"
fi

echo ""
echo "--- Phase 4: a change outside web/ ---"
check "docs-only tag needs no recording" 1 "$NEEDED" v3
check "the older web/ tree does" 0 "$NEEDED" v1

echo ""
echo "--- Phase 5: a label that is not a revision ---"
check "publish main@abc1234" 0 "$PUBLISH" hero.webp main@abc1234
git -C "$ORIGIN" log -1 --format=%B readme-assets > "${DIR}/message.txt"
assert_file_contains "${DIR}/message.txt" "hero: main@abc1234"
assert_file_contains "${DIR}/message.txt" "Web-Tree: $(git rev-parse HEAD:web)"

echo ""
if [[ "${E2E_FAILURES}" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
