#!/usr/bin/env bash
# scenarios/23_film_publish.sh — the README films' orphan branch (D1068).
#
# The films live on `readme-film`, one force-pushed commit carrying a Film-Tree:
# trailer, and the release candidate renders them only when the hash of the films'
# inputs (film/inputs.sh) differs from the trailer. This drives film/publish.sh and
# film/film-needed.sh against a scratch repository and a bare "origin" (no
# network, no browser).
#
# Verifies:
#   1. With no branch a render is needed (exit 0).
#   2. After a publish the same revision needs none (exit 1).
#   3. A change in each class of input (film/, graph3d/, palette.ts, theme.ts,
#      package-lock.json) needs a render.
#   4. A change outside the set (README.md, another web/src file) needs none.
#   5. Publishing twice leaves one commit with README.md, both films (two fixture
#      files: the launch film's film.webp and another film) and the trailer,
#      and main's history untouched.
#   6. With no argument, publish takes every film/out/*.webp present, still in
#      one commit; with none present it is exit 1.
#   7. An input missing at the revision is exit 2 naming it; a bad revision is 2.
#   8. Only the names film/films.txt allows are published (the render job's file
#      names are data it chose, D1068): a file not in the list, a name holding a
#      newline that would add a tree entry, a symlink, are exit 1 and push
#      nothing; films.txt without a final newline or with CRLF still reads; a
#      NUL byte in films.txt is refused by publish.sh and by render.sh, which
#      stops before it starts vite.
#   9. A file over its film's cap (film/max-bytes.txt, the file record.mjs reads
#      too; FILM_MAX_BYTES overrides it) is exit 1 naming the file, its size and
#      the cap, before any file is hashed and with nothing pushed; a file exactly
#      at the cap is published; a bad override or cap file is exit 1, and the
#      grammar is the one of record.mjs: a no-break space, VT, FF or inner CR as
#      separator, a value of 16 digits or past int64, and a NUL byte (which bash
#      `read` would cut the line at) in film/max-bytes.txt or film/films.txt, are
#      refused.
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
cp "${REPO_ROOT}/film/publish.sh" "${REPO_ROOT}/film/render.sh" "${REPO_ROOT}/film/film-needed.sh" "${REPO_ROOT}/film/inputs.sh" "${REPO_ROOT}/film/max-bytes.txt" film/
printf 'launch\nplugins\ndeployment\n' > film/films.txt
echo main > film/src/main.ts
echo scene > web/src/lib/graph3d/scene.ts
echo palette > web/src/lib/palette.ts
echo theme > web/src/lib/theme.ts
echo api > web/src/lib/api.ts
echo tokens > web/src/styles/tokens.css
echo brand > docs/brand/cartographer.css
echo pine > docs/brand/coordinate-pine.svg
echo lock > web/package-lock.json
echo doc-1 > README.md
git add . && git commit -qm "film 1"
git remote add origin "$ORIGIN"
head -c 4096 /dev/urandom > film.webp
head -c 4096 /dev/urandom > plugins.webp
PUBLISH=(film/publish.sh film.webp plugins.webp)
NEEDED=(film/film-needed.sh HEAD)

echo ""
echo "--- Phase 1: no branch yet ---"
check "a render is needed" 0 "${NEEDED[@]}"
assert_file_contains "${DIR}/out.txt" "no readme-film branch"

echo ""
echo "--- Phase 2: after a publish ---"
check "publish" 0 "${PUBLISH[@]}"
check "the same revision needs no render" 1 "${NEEDED[@]}"
assert_file_contains "${DIR}/out.txt" "film tree $(git ls-tree -r --full-tree HEAD -- film web/src/lib/graph3d web/src/lib/palette.ts web/src/lib/theme.ts web/src/styles/tokens.css docs/brand/cartographer.css docs/brand/coordinate-pine.svg web/package-lock.json | git hash-object --stdin)"

echo ""
echo "--- Phase 3: a change in each class of input ---"
for f in film/src/main.ts web/src/lib/graph3d/scene.ts web/src/lib/palette.ts web/src/lib/theme.ts web/src/styles/tokens.css docs/brand/cartographer.css docs/brand/coordinate-pine.svg web/package-lock.json; do
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
assert_file_contains "${DIR}/tree.txt" "plugins.webp"
assert_file_contains "${DIR}/tree.txt" "README.md"
if [[ "$(git --git-dir="$ORIGIN" rev-parse readme-film:film.webp)" == "$(git hash-object film.webp)" &&
    "$(git --git-dir="$ORIGIN" rev-parse readme-film:plugins.webp)" == "$(git hash-object plugins.webp)" ]]; then
    _assert_pass "each file is in the tree with its own content"
else
    _assert_fail "a file in the tree is not the one published"
fi
if git --git-dir="$ORIGIN" rev-parse --verify -q main >/dev/null; then
    _assert_fail "the publish pushed to main"
else
    _assert_pass "main was not pushed"
fi

echo ""
echo "--- Phase 6: no argument publishes every film/out/*.webp ---"
check "no film rendered is exit 1" 1 film/publish.sh
assert_file_contains "${DIR}/out.txt" "no film/out/*.webp"
mkdir -p film/out
head -c 2048 /dev/urandom > film/out/film.webp
head -c 2048 /dev/urandom > film/out/deployment.webp
check "publish with no argument" 0 film/publish.sh
git --git-dir="$ORIGIN" ls-tree --name-only readme-film > "${DIR}/tree.txt"
assert_file_contains "${DIR}/tree.txt" "film.webp"
assert_file_contains "${DIR}/tree.txt" "deployment.webp"
assert_file_contains "${DIR}/tree.txt" "README.md"
if grep -q "plugins.webp" "${DIR}/tree.txt"; then
    _assert_fail "the earlier plugins.webp survived a publish that did not carry it"
else
    _assert_pass "the branch holds exactly the files of the last publish"
fi
COMMITS="$(git --git-dir="$ORIGIN" rev-list --count readme-film)"
if [[ "$COMMITS" -eq 1 ]]; then
    _assert_pass "readme-film still has exactly one commit"
else
    _assert_fail "readme-film has ${COMMITS} commits, want 1"
fi
rm -r film/out

echo ""
echo "--- Phase 7: only the films of film/films.txt are published ---"
BEFORE="$(git --git-dir="$ORIGIN" rev-parse readme-film)"
# unchanged <message>: the branch is the commit it was before the refused publish
unchanged() {
    if [[ "$(git --git-dir="$ORIGIN" rev-parse readme-film)" == "$BEFORE" ]]; then
        _assert_pass "$1: nothing was pushed"
    else
        _assert_fail "$1: readme-film moved"
    fi
}
mkdir -p film/out
head -c 2048 /dev/urandom > film/out/film.webp
head -c 2048 /dev/urandom > film/out/extra.webp
check "no argument refuses a file the list does not name" 1 film/publish.sh
assert_file_contains "${DIR}/out.txt" "extra.webp"
unchanged "an unlisted file"
rm film/out/extra.webp
# A name that carries a second `git mktree` line: a symlink entry, if it were let through.
EVIL="$(printf 'x.webp\n120000 blob %s\ty.webp' "$(printf '/etc/passwd' | git hash-object -w --stdin)")"
cp film/out/film.webp "film/out/${EVIL}"
check "no argument refuses a name holding a newline" 1 film/publish.sh
unchanged "a name holding a newline"
check "an argument holding a newline is refused" 1 film/publish.sh "film/out/${EVIL}"
unchanged "an argument holding a newline"
rm "film/out/${EVIL}"
check "an argument the list does not name is refused" 1 film/publish.sh film/out/film.webp film/out/extra.webp
unchanged "an argument not in the list"
rm film/out/film.webp
head -c 2048 /dev/urandom > real.bin
ln -s ../../real.bin film/out/film.webp
check "a symlink is refused (no argument)" 1 film/publish.sh
unchanged "a symlink"
check "a symlink is refused (argument)" 1 film/publish.sh film/out/film.webp
unchanged "a symlink argument"
rm film/out/film.webp real.bin
rmdir film/out
# The list as an editor on Windows leaves it: CRLF, no final newline.
printf 'launch\r\nplugins' > film/films.txt
check "films.txt with CRLF and no final newline still names the films" 0 "${PUBLISH[@]}"
git checkout -q film/films.txt
# bash `read` would stop at the NUL byte: `launch<NUL>junk` as `launch`, a line of NUL alone as blank. record.mjs and the Go test
# keep or refuse it, so the list is refused.
BEFORE="$(git --git-dir="$ORIGIN" rev-parse readme-film)"
printf 'launch\000junk\nplugins\n' > film/films.txt
check "films.txt with a NUL inside a name is refused" 1 "${PUBLISH[@]}"
assert_file_contains "${DIR}/out.txt" "NUL byte"
printf 'launch\nplugins\n\000\n' > film/films.txt
check "films.txt with a line of NUL alone is refused" 1 "${PUBLISH[@]}"
unchanged "a films.txt with a NUL byte"
# render.sh reads the same list and refuses the NUL byte before it starts vite: the scratch repo has no web/node_modules, so a
# render that got past the check would fail with another message, and film/out (made just before vite starts) must not exist.
for nul in 'launch\000junk\nplugins\n' 'launch\nplugins\n\000\n'; do
    printf "$nul" > film/films.txt
    check "render.sh refuses a films.txt with a NUL byte" 1 film/render.sh
    assert_file_contains "${DIR}/out.txt" "render: film/films.txt holds a NUL byte"
    if grep -q "vite" "${DIR}/out.txt"; then
        _assert_fail "render.sh went on to start vite: $(cat "${DIR}/out.txt")"
    elif [[ -e film/out ]]; then
        _assert_fail "render.sh made film/out, which it does only just before it starts vite"
    else
        _assert_pass "render.sh stopped before it started vite"
    fi
done
git checkout -q film/films.txt

echo ""
echo "--- Phase 8: a file over its film's cap is refused ---"
# Small caps in the scratch copy of the shared file keep the fixtures tiny: launch 5000, plugins 4000, any other film 3000.
printf '# caps\ndefault 3000\nlaunch 5000\nplugins 4000\n' > film/max-bytes.txt
BEFORE="$(git --git-dir="$ORIGIN" rev-parse readme-film)"
# fixture <file> <bytes>: random content, so no earlier publish wrote its blob
fixture() { head -c "$2" /dev/urandom > "$1"; }
# unwritten <file>: its blob is not in the object database, so the refused publish hashed nothing
unwritten() {
    if git cat-file -e "$(git hash-object "$1")" 2>/dev/null; then
        _assert_fail "$1 was hashed into the repository before the refusal"
    else
        _assert_pass "$1 was not hashed before the refusal"
    fi
}
mkdir -p caps
fixture caps/film.webp 5001
check "a launch film one byte over its cap is refused" 1 film/publish.sh caps/film.webp
assert_file_contains "${DIR}/out.txt" "caps/film.webp"
assert_file_contains "${DIR}/out.txt" "5001 bytes"
assert_file_contains "${DIR}/out.txt" "cap of 5000 bytes"
unchanged "an oversized launch film"
fixture caps/film.webp 5000
fixture caps/plugins.webp 4001
check "a film with a line of its own is refused over it" 1 film/publish.sh caps/film.webp caps/plugins.webp
assert_file_contains "${DIR}/out.txt" "caps/plugins.webp"
assert_file_contains "${DIR}/out.txt" "cap of 4000 bytes"
unchanged "an oversized plugins film"
unwritten caps/film.webp
fixture caps/plugins.webp 4000
fixture caps/deployment.webp 3001
check "a film with no line takes the default cap" 1 film/publish.sh caps/film.webp caps/plugins.webp caps/deployment.webp
assert_file_contains "${DIR}/out.txt" "caps/deployment.webp"
assert_file_contains "${DIR}/out.txt" "cap of 3000 bytes"
unchanged "an oversized film at the default cap"
unwritten caps/film.webp
unwritten caps/plugins.webp
mkdir -p film/out
cp caps/film.webp film/out/film.webp
fixture film/out/deployment.webp 3001
check "no argument refuses an oversized film too" 1 film/publish.sh
assert_file_contains "${DIR}/out.txt" "film/out/deployment.webp"
unchanged "an oversized film/out file"
rm -r film/out
fixture caps/deployment.webp 3000
check "files exactly at their caps are published" 0 film/publish.sh caps/film.webp caps/plugins.webp caps/deployment.webp
if [[ "$(git --git-dir="$ORIGIN" rev-parse readme-film:deployment.webp)" == "$(git hash-object caps/deployment.webp)" ]]; then
    _assert_pass "the file at the cap is in the tree"
else
    _assert_fail "the file at the cap is not in the tree"
fi
BEFORE="$(git --git-dir="$ORIGIN" rev-parse readme-film)"
fixture caps/film.webp 5001
check "FILM_MAX_BYTES lowers every cap" 1 env FILM_MAX_BYTES=100 film/publish.sh caps/plugins.webp
assert_file_contains "${DIR}/out.txt" "cap of 100 bytes"
unchanged "a file over FILM_MAX_BYTES"
check "FILM_MAX_BYTES raises every cap" 0 env FILM_MAX_BYTES=6000 film/publish.sh caps/film.webp caps/plugins.webp
BEFORE="$(git --git-dir="$ORIGIN" rev-parse readme-film)"
# 16 digits, and a value past int64, are refused as the others: bash [ -le ] would error on them and report a false "over the cap"
for bad in abc 0 -5 1.5 05 1234567890123456 9223372036854775808; do
    check "FILM_MAX_BYTES=${bad} is refused" 1 env FILM_MAX_BYTES="$bad" film/publish.sh caps/plugins.webp
    assert_file_contains "${DIR}/out.txt" "FILM_MAX_BYTES must be a positive number of bytes"
done
unchanged "a bad FILM_MAX_BYTES"
check "FILM_MAX_BYTES of 15 digits is accepted" 0 env FILM_MAX_BYTES=999999999999999 film/publish.sh caps/plugins.webp
BEFORE="$(git --git-dir="$ORIGIN" rev-parse readme-film)"
printf 'launch 5000\n' > film/max-bytes.txt
check "a cap file with no default is refused" 1 film/publish.sh caps/plugins.webp
assert_file_contains "${DIR}/out.txt" "no \"default\" line"
printf 'default 3000\nlaunch lots\n' > film/max-bytes.txt
check "a cap that is not a number is refused" 1 film/publish.sh caps/plugins.webp
assert_file_contains "${DIR}/out.txt" "launch lots"
unchanged "a bad cap file"
# One grammar with record.mjs and internal/repodocs/film_test.go: spaces or tabs separate, a CR at the end of the line is dropped,
# and nothing else (a no-break space, a vertical tab, a form feed, a CR inside the line) is a separator.
fixture caps/film.webp 5000
printf 'default 3000\r\n# caps\r\n\r\n \tlaunch\t 5000 \t\r\n' > film/max-bytes.txt
check "a cap file with tabs and CRLF is read" 0 film/publish.sh caps/film.webp
BEFORE="$(git --git-dir="$ORIGIN" rev-parse readme-film)"
for sep in $'\xc2\xa0' $'\v' $'\f' $'\r'; do
    printf 'default 3000\nlaunch%s5000\n' "$sep" > film/max-bytes.txt
    check "a cap line separated by $(printf %q "$sep") is refused" 1 film/publish.sh caps/film.webp
    assert_file_contains "${DIR}/out.txt" "is not \"<film> <positive number of bytes"
done
printf 'default 3000\nlaunch 5000\r\r\n' > film/max-bytes.txt
check "a cap line with two CRs at its end is refused" 1 film/publish.sh caps/film.webp
printf 'default 3000\nlaunch 1234567890123456\n' > film/max-bytes.txt
check "a 16-digit cap is refused" 1 film/publish.sh caps/film.webp
assert_file_contains "${DIR}/out.txt" "launch 1234567890123456"
printf 'default 3000\nlaunch 9223372036854775808\n' > film/max-bytes.txt
check "a cap past int64 is refused" 1 film/publish.sh caps/film.webp
unchanged "a cap file outside the grammar"
# bash `read` stops a line at a NUL byte: `default 5<NUL>junk` would read as `default 5`, a line of NUL alone as blank;
# record.mjs and the Go parseFilmCaps refuse both, so publish.sh does too, before it parses a line.
printf 'default 3000\nlaunch 5000\000junk\n' > film/max-bytes.txt
check "a cap line with a NUL after its bytes is refused" 1 film/publish.sh caps/film.webp
assert_file_contains "${DIR}/out.txt" "NUL byte"
printf 'default 3000\n\000\nlaunch 5000\n' > film/max-bytes.txt
check "a cap line of NUL alone is refused" 1 film/publish.sh caps/film.webp
assert_file_contains "${DIR}/out.txt" "NUL byte"
printf '# a\000b\ndefault 3000\n' > film/max-bytes.txt
check "a NUL byte inside a comment line of the cap file is refused" 1 film/publish.sh caps/film.webp
assert_file_contains "${DIR}/out.txt" "NUL byte"
unchanged "a cap file holding a NUL byte"
git checkout -q film/max-bytes.txt
rm -r caps

echo ""
echo "--- Phase 9: an input missing, a bad revision ---"
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
