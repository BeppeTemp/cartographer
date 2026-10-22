#!/usr/bin/env bash
# Publishes the README's launch film on the orphan branch `readme-film` (D807): one commit holding
# film.webp, force-pushed, like the hero on `readme-assets` (D790) but on a branch of its own, so the
# hero workflow's force-push never drops it and the film never grows the history of main.
#
# The commit carries a `Film-Tree:` trailer, the hash of the film's inputs at HEAD (film/inputs.sh), which
# film-needed.sh compares to decide whether the film must be rendered again (D1068).
#
# Usage, from the repository root after a full render (film/README.md): film/publish.sh [file]
# bash 3.2 compatible: it runs on macOS too.
set -euo pipefail
# shellcheck source=film/inputs.sh
. "$(dirname "${BASH_SOURCE[0]}")/inputs.sh"
FILE="${1:-film/out/film.webp}"
[ -s "$FILE" ] || { echo "publish: ${FILE} is missing or empty, render it first (film/README.md)" >&2; exit 1; }
BLOB="$(git hash-object -w "$FILE")"
NOTE="$(printf '%s\n' \
    'The README launch film lives here: written by film/publish.sh (D807) and force-pushed on every re-render, so this branch is always one commit. Do not commit to it.' \
    | git hash-object -w --stdin)"
ROOT="$(printf '100644 blob %s\tREADME.md\n100644 blob %s\tfilm.webp\n' "$NOTE" "$BLOB" | git mktree)"
TREE="$(film_tree HEAD)"
COMMIT="$(printf 'film: rendered from %s\n\nFilm-Tree: %s\n' "$(git rev-parse --short HEAD)" "$TREE" | git commit-tree "$ROOT")"
git push --force origin "${COMMIT}:refs/heads/readme-film"
echo "publish: readme-film is ${COMMIT} (film tree ${TREE})"
