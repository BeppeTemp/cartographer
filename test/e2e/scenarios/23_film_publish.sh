#!/usr/bin/env bash
# scenarios/23_film_publish.sh — the README film's orphan branch (D1068).
#
# The film lives on `readme-film`, one force-pushed commit, and the release
# candidate renders it again only when the film's inputs (film/inputs.sh)
# differ from the ones it was rendered from. This drives film/publish.sh and
# film/film-needed.sh against a scratch repository and a bare "origin" (no
# network, no binary).
#
# Verifies:
#   1. With no branch a render is needed (exit 0).
#   2. After a publish the same revision needs none (exit 1).
#   3. A change in each class of input (film/, graph3d/, palette.ts,
#      package-lock.json) needs one; a change outside the set needs none.
#   4. Publishing twice leaves exactly one commit, with the Film-Tree trailer
#      and the two files, and main's history untouched.
#   5. An input missing at the revision is a usage error (exit 2).
#
# Expected environment variables: E2E_TMP_DIR, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# shellcheck source=../lib/assert.sh
source "${E2E_DIR}/lib/assert.sh"

SCENARIO_NAME="23_film_publish"

echo "=== Scenario ${SCENARIO_NAME} ==="

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
PUBLISH="${REPO_ROOT}/film/publish.sh"
NEEDED="${REPO_ROOT}/film/film-needed.sh"
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

# edit <path> <tag>: change a file and commit it under <tag>.
edit() {
    echo "$2" >> "$1"
    git commit -qam "$2" && git tag "$2"
}

mkdir -p "$DIR"
git init -q --bare "$ORIGIN"
git init -q -b main "$WORK"
cd "$WORK" || exit 1
git config user.name e2e
git config user.email e2e@example.com
mkdir -p film web/src/lib/graph3d web/src/styles docs/brand
echo a > film/main.ts
echo a > web/src/lib/graph3d/scene.ts
echo a > web/src/lib/palette.ts
echo a > web/src/lib/theme.ts
echo a > web/src/lib/other.ts
echo a > web/src/styles/tokens.css
echo a > docs/brand/cartographer.css
echo a > docs/brand/coordinate-pine.svg
echo a > web/package-lock.json
echo doc > README.md
git add . && git commit -qm base && git tag base
git remote add origin "$ORIGIN"
head -c 4096 /dev/urandom > film.webp

echo ""
echo "--- Phase 1: no branch yet ---"
check "a render is needed" 0 "$NEEDED" HEAD
assert_file_contains "${DIR}/out.txt" "no readme-film branch"

echo ""
echo "--- Phase 2: after a publish ---"
check "publish" 0 "$PUBLISH" film.webp
check "the same revision needs no render" 1 "$NEEDED" HEAD

echo ""
echo "--- Phase 3: classes of input ---"
edit README.md docs-only
check "a change outside the set needs no render" 1 "$NEEDED" HEAD
edit web/src/lib/other.ts other-web-file
check "another web/src file needs no render" 1 "$NEEDED" HEAD
for f in film/main.ts web/src/lib/graph3d/scene.ts web/src/lib/palette.ts web/package-lock.json; do
    git reset -q --hard base
    edit "$f" "change-$(basename "$f")"
    check "a change to ${f} needs a render" 0 "$NEEDED" HEAD
done
git reset -q --hard base

echo ""
echo "--- Phase 4: one commit, whatever is published ---"
check "publish again" 0 "$PUBLISH" film.webp
COMMITS="$(git -C "$ORIGIN" rev-list --count readme-film)"
if [[ "$COMMITS" -eq 1 ]]; then
    _assert_pass "readme-film has exactly one commit"
else
    _assert_fail "readme-film has ${COMMITS} commits, want 1"
fi
git -C "$ORIGIN" log -1 --format=%B readme-film > "${DIR}/message.txt"
assert_file_contains "${DIR}/message.txt" "film: rendered from"
assert_file_contains "${DIR}/message.txt" "Film-Tree: "
git -C "$ORIGIN" ls-tree --name-only readme-film > "${DIR}/tree.txt"
assert_file_contains "${DIR}/tree.txt" "film.webp"
assert_file_contains "${DIR}/tree.txt" "README.md"
if git -C "$ORIGIN" rev-parse --verify -q main >/dev/null; then
    _assert_fail "the publish pushed to main"
else
    _assert_pass "main was not pushed"
fi

echo ""
echo "--- Phase 5: an input missing at the revision ---"
git rm -q web/src/lib/palette.ts && git commit -qm "drop palette"
check "a renamed input is a usage error" 2 "$NEEDED" HEAD
assert_file_contains "${DIR}/out.txt" "web/src/lib/palette.ts"
check "a bad revision is a usage error" 2 "$NEEDED" no-such-rev

echo ""
if [[ "${E2E_FAILURES}" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
