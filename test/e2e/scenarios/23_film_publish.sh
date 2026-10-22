#!/usr/bin/env bash
# scenarios/23_film_publish.sh — the README film's orphan branch (D1068).
#
# The film lives on `readme-film`, one force-pushed commit carrying a Film-Tree:
# trailer, and the release candidate renders it only when the hash of the film's
# inputs (film/inputs.sh) differs from the trailer. This drives film/publish.sh and
# film/film-needed.sh against a scratch repository and a bare "origin" (no
# network, no browser).
#
# Verifies:
#   1. With no branch a render is needed (exit 0).
#   2. After a publish the same revision needs none (exit 1).
#   3. A change in each class of input (film/, graph3d/, palette.ts,
#      package-lock.json) needs a render.
#   4. A change outside the set (README.md, another web/src file) needs none.
#   5. Publishing twice leaves one commit with README.md, film.webp and the
#      trailer, and main's history untouched.
#   6. An input missing at the revision is exit 2 naming it; a bad revision is 2.
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

# change <file>: a new commit that edits one file
change() {
    echo "$RANDOM$RANDOM" >> "$1"
    git commit -qam "change $1"
}

mkdir -p "$DIR"
git init -q --bare "$ORIGIN"
git init -q -b main "$WORK"
cd "$WORK" || exit 1
git config user.name e2e
git config user.email e2e@example.com

# The scripts under test, and the paths film/inputs.sh lists.
mkdir -p film/src web/src/lib/graph3d docs/brand web/src/styles
cp "${REPO_ROOT}/film/publish.sh" "${REPO_ROOT}/film/film-needed.sh" "${REPO_ROOT}/film/inputs.sh" film/
echo main > film/src/main.ts
echo scene > web/src/lib/graph3d/scene.ts
echo palette > web/src/lib/palette.ts
echo api > web/src/lib/api.ts
echo tokens > web/src/styles/tokens.css
echo brand > docs/brand/cartographer.css
echo pine > docs/brand/coordinate-pine.svg
echo lock > web/package-lock.json
echo doc-1 > README.md
git add . && git commit -qm "film 1"
git remote add origin "$ORIGIN"
head -c 4096 /dev/urandom > film.webp
PUBLISH=(film/publish.sh film.webp)
NEEDED=(film/film-needed.sh HEAD)

echo ""
echo "--- Phase 1: no branch yet ---"
check "a render is needed" 0 "${NEEDED[@]}"
assert_file_contains "${DIR}/out.txt" "no readme-film branch"

echo ""
echo "--- Phase 2: after a publish ---"
check "publish" 0 "${PUBLISH[@]}"
check "the same revision needs no render" 1 "${NEEDED[@]}"
assert_file_contains "${DIR}/out.txt" "film tree $(git ls-tree -r --full-tree HEAD -- film web/src/lib/graph3d web/src/lib/palette.ts web/src/styles/tokens.css docs/brand/cartographer.css docs/brand/coordinate-pine.svg web/package-lock.json | git hash-object --stdin)"

echo ""
echo "--- Phase 3: a change in each class of input ---"
for f in film/src/main.ts web/src/lib/graph3d/scene.ts web/src/lib/palette.ts web/src/styles/tokens.css docs/brand/cartographer.css docs/brand/coordinate-pine.svg web/package-lock.json; do
    change "$f"
    check "a change to ${f} needs a render" 0 "${NEEDED[@]}"
    git reset -q --hard HEAD~1
done
check "back to the published sources: no render" 1 "${NEEDED[@]}"

echo ""
echo "--- Phase 4: a change outside the set ---"
change README.md
change web/src/lib/api.ts
check "README.md and another web/src file need no render" 1 "${NEEDED[@]}"

echo ""
echo "--- Phase 5: one commit, whatever is published ---"
check "publish again" 0 "${PUBLISH[@]}"
check "publish a third time" 0 "${PUBLISH[@]}"
COMMITS="$(git --git-dir="$ORIGIN" rev-list --count readme-film)"
if [[ "$COMMITS" -eq 1 ]]; then
    _assert_pass "readme-film has exactly one commit"
else
    _assert_fail "readme-film has ${COMMITS} commits, want 1"
fi
git --git-dir="$ORIGIN" log -1 --format=%B readme-film > "${DIR}/message.txt"
assert_file_contains "${DIR}/message.txt" "film: rendered from $(git rev-parse --short HEAD)"
assert_file_contains "${DIR}/message.txt" "Film-Tree: "
git --git-dir="$ORIGIN" ls-tree --name-only readme-film > "${DIR}/tree.txt"
assert_file_contains "${DIR}/tree.txt" "film.webp"
assert_file_contains "${DIR}/tree.txt" "README.md"
if git --git-dir="$ORIGIN" rev-parse --verify -q main >/dev/null; then
    _assert_fail "the publish pushed to main"
else
    _assert_pass "main was not pushed"
fi

echo ""
echo "--- Phase 6: an input missing, a bad revision ---"
git rm -q web/src/lib/palette.ts && git commit -qm "rename palette"
check "a missing input is exit 2" 2 "${NEEDED[@]}"
assert_file_contains "${DIR}/out.txt" "web/src/lib/palette.ts"
check "a bad revision is exit 2" 2 film/film-needed.sh no-such-rev
check "no argument is exit 2" 2 film/film-needed.sh
check "publish refuses a missing input" 2 "${PUBLISH[@]}"

echo ""
if [[ "${E2E_FAILURES}" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
