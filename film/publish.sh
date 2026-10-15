#!/usr/bin/env bash
# Publishes the README's launch film on the orphan branch `readme-film` (D807): one commit holding
# film.webp, force-pushed, like the hero on `readme-assets` (D790) but on a branch of its own, so the
# hero workflow's force-push never drops it and the film never grows the history of main.
#
# Usage, from the repository root after a full render (film/README.md): film/publish.sh [file]
# bash 3.2 compatible: it runs on macOS too.
set -euo pipefail
FILE="${1:-film/out/film.webp}"
[ -s "$FILE" ] || { echo "publish: ${FILE} is missing or empty, render it first (film/README.md)" >&2; exit 1; }
BLOB="$(git hash-object -w "$FILE")"
NOTE="$(printf '%s\n' \
    'The README launch film lives here: written by film/publish.sh (D807) and force-pushed on every re-render, so this branch is always one commit. Do not commit to it.' \
    | git hash-object -w --stdin)"
ROOT="$(printf '100644 blob %s\tREADME.md\n100644 blob %s\tfilm.webp\n' "$NOTE" "$BLOB" | git mktree)"
COMMIT="$(printf 'film: rendered from %s\n' "$(git rev-parse --short HEAD)" | git commit-tree "$ROOT")"
git push --force origin "${COMMIT}:refs/heads/readme-film"
echo "publish: readme-film is ${COMMIT}"
