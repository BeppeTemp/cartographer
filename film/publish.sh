#!/usr/bin/env bash
# Publishes the README's films on the orphan branch `readme-film` (D807): one commit holding every film's WebP,
# force-pushed, like the hero on `readme-assets` (D790) but on a branch of its own, so the hero workflow's
# force-push never drops it and the films never grow the history of main.
#
# The commit carries a `Film-Tree:` trailer, the hash of the films' inputs at HEAD (film/inputs.sh), which
# film-needed.sh compares to decide whether the films must be rendered again (D1068). One hash covers the whole set.
#
# Usage, from the repository root after a full render (film/README.md): film/publish.sh [file...]
# Only the files film/films.txt allows go on the branch: film.webp for the launch film, <name>.webp for each other
# film. With no argument it takes, for each listed film, its file in film/out when present, and refuses any other
# film/out/*.webp; with arguments, each file's name must be an allowed one. Each file is a regular file, not a
# symlink and no larger than its film's cap (film/max-bytes.txt, FILM_MAX_BYTES overrides it, as in record.mjs): the
# render job already checks it, but it runs code of an untrusted ref, so this job checks again, before anything is
# hashed. It goes into the tree under its own name (film/out/film.webp is film.webp, the launch film's URL).
# bash 3.2 compatible: it runs on macOS too.
set -euo pipefail
# shellcheck source=film/inputs.sh
. "$(dirname "${BASH_SOURCE[0]}")/inputs.sh"

# bash `read` stops a line at a NUL byte, so `launch<NUL>junk` would read as `launch` and a line of NUL alone as blank, where
# record.mjs, vite.config.ts and internal/repodocs/film_test.go keep or refuse it. A NUL byte in a file read below is refused
# before its loop; LC_ALL=C because a BSD tr fails on a byte that is not valid UTF-8. wc and tr -d are in macOS and Linux.
nonul() {
    local total clean
    total="$(wc -c < "$1")"
    clean="$(LC_ALL=C tr -d '\000' < "$1" | wc -c)"
    [ "$((total))" -eq "$((clean))" ]
}

# The films, read as film/render.sh reads them (last line without a newline, CRLF, blanks around a name).
nonul film/films.txt || { echo "publish: film/films.txt holds a NUL byte, which is not in a film name" >&2; exit 1; }
FILMS=()
while IFS= read -r name || [ -n "$name" ]; do
    name="${name//$'\r'/}"
    name="${name#"${name%%[![:space:]]*}"}"
    name="${name%"${name##*[![:space:]]}"}"
    if [ -n "$name" ]; then FILMS+=("$name"); fi
done < film/films.txt
[ "${#FILMS[@]}" -gt 0 ] || { echo "publish: film/films.txt lists no film" >&2; exit 1; }

# The file name a film is published under: the launch film keeps the name the README embeds.
ALLOWED=()
for name in "${FILMS[@]}"; do
    if [ "$name" = launch ]; then ALLOWED+=(film.webp); else ALLOWED+=("${name}.webp"); fi
done
allowed() { local a; for a in "${ALLOWED[@]}"; do [ "$a" = "$1" ] && return 0; done; return 1; }

# The size cap of each film, from the file record.mjs reads too (film/max-bytes.txt: `<film> <bytes>`, `default` for a
# film with no line). The one grammar of record.mjs, publish.sh and internal/repodocs/film_test.go: a line is
# `<key>` and `<bytes>` split by spaces or tabs, with spaces or tabs around it, a CR at the end of the line dropped;
# a blank line or one whose first character after the spaces and tabs is # is skipped. bytes is 1 to 15 digits, no leading
# zero (so it fits every parser), here and in FILM_MAX_BYTES, which when set replaces every film's cap. Anything else, a
# no-break space or a vertical tab as separator included, is the same error.
BYTES='[1-9][0-9]{0,14}'
WS=$' \t'
BLANKRE="^[${WS}]*(#|$)"
CAPRE="^[${WS}]*([^${WS}]+)[${WS}]+(${BYTES})[${WS}]*$"
nonul film/max-bytes.txt || { echo 'publish: film/max-bytes.txt holds a NUL byte, which is not in "<film> <positive number of bytes, at most 15 digits>"' >&2; exit 1; }
CAPKEYS=()
CAPVALS=()
while IFS= read -r line || [ -n "$line" ]; do
    line="${line%$'\r'}"
    if [[ "$line" =~ $BLANKRE ]]; then continue; fi
    [[ "$line" =~ $CAPRE ]] || { printf 'publish: film/max-bytes.txt: "%s" is not "<film> <positive number of bytes, at most 15 digits>"\n' "$line" >&2; exit 1; }
    CAPKEYS+=("${BASH_REMATCH[1]}")
    CAPVALS+=("${BASH_REMATCH[2]}")
done < film/max-bytes.txt
# The last line for a name wins, as in record.mjs.
cap_of() {
    local i want="$1" found="" dflt=""
    for i in "${!CAPKEYS[@]}"; do
        if [ "${CAPKEYS[$i]}" = "$want" ]; then found="${CAPVALS[$i]}"; fi
        if [ "${CAPKEYS[$i]}" = default ]; then dflt="${CAPVALS[$i]}"; fi
    done
    [ -n "$dflt" ] || { echo 'publish: film/max-bytes.txt has no "default" line' >&2; return 1; }
    printf '%s' "${found:-$dflt}"
}
cap_of default >/dev/null || exit 1
MAXENV="${FILM_MAX_BYTES:-}"
if [ -n "$MAXENV" ] && ! [[ "$MAXENV" =~ ^${BYTES}$ ]]; then
    printf 'publish: FILM_MAX_BYTES must be a positive number of bytes, at most 15 digits, got "%s"\n' "$MAXENV" >&2
    exit 1
fi

# Why only the listed names (D1068): the files come from the render job, which runs code of an untrusted ref, so
# a file name is data it chose. `git mktree` below reads one line per entry, `<mode> blob <id><TAB><name>`, so a
# name holding a newline would add entries of its own (a symlink to anywhere, say) to the tree that is
# force-pushed. The names allowed are the ones the checked-out commit's film/films.txt lists, which that job does
# not control, and a name never goes into the tree otherwise.
FILES=()
if [ "$#" -eq 0 ]; then
    for f in film/out/*.webp; do
        if [ -e "$f" ] || [ -L "$f" ]; then
            allowed "${f##*/}" || { printf 'publish: %q is not a film of film/films.txt (allowed: %s)\n' "$f" "${ALLOWED[*]}" >&2; exit 1; }
        fi
    done
    for name in "${ALLOWED[@]}"; do
        if [ -e "film/out/${name}" ] || [ -L "film/out/${name}" ]; then FILES+=("film/out/${name}"); fi
    done
    [ "${#FILES[@]}" -gt 0 ] || { echo "publish: no film/out/*.webp, render the films first (film/README.md)" >&2; exit 1; }
else
    for f in "$@"; do
        allowed "${f##*/}" || { printf 'publish: %q is not a film of film/films.txt (allowed: %s)\n' "$f" "${ALLOWED[*]}" >&2; exit 1; }
        FILES+=("$f")
    done
fi

# Every file is checked before the first is hashed, so a refused publish leaves no object behind. The cap is the one of
# the film the file name stands for (film.webp is launch, <name>.webp is <name>).
for f in "${FILES[@]}"; do
    [ -f "$f" ] && [ ! -L "$f" ] || { printf 'publish: %q is not a regular file, render it first (film/README.md)\n' "$f" >&2; exit 1; }
    [ -s "$f" ] || { echo "publish: ${f} is empty, render it first (film/README.md)" >&2; exit 1; }
    name="${f##*/}"
    if [ "$name" = film.webp ]; then film=launch; else film="${name%.webp}"; fi
    cap="${MAXENV:-$(cap_of "$film")}"
    size="$(wc -c < "$f")"
    size="${size//[[:space:]]/}"
    [ "$size" -le "$cap" ] || { printf 'publish: %q is %s bytes, over the cap of %s bytes (film/max-bytes.txt, FILM_MAX_BYTES): shorten the film or raise the cap, nothing was published\n' "$f" "$size" "$cap" >&2; exit 1; }
done

ENTRIES=""
SEEN=" "
for f in "${FILES[@]}"; do
    name="${f##*/}"
    case "$SEEN" in
    *" ${name} "*) echo "publish: two files are named ${name}" >&2; exit 1 ;;
    esac
    SEEN="${SEEN}${name} "
    ENTRIES="${ENTRIES}$(printf '100644 blob %s\t%s\n' "$(git hash-object -w "$f")" "$name")
"
done
NOTE="$(printf '%s\n' \
    'The README films live here: written by film/publish.sh (D807) and force-pushed on every re-render, so this branch is always one commit. Do not commit to it.' \
    | git hash-object -w --stdin)"
ROOT="$(printf '100644 blob %s\tREADME.md\n%s' "$NOTE" "$ENTRIES" | git mktree)"
TREE="$(film_tree HEAD)"
COMMIT="$(printf 'film: rendered from %s\n\nFilm-Tree: %s\n' "$(git rev-parse --short HEAD)" "$TREE" | git commit-tree "$ROOT")"
git push --force origin "${COMMIT}:refs/heads/readme-film"
echo "publish: readme-film is ${COMMIT} (film tree ${TREE}; ${SEEN# })"
