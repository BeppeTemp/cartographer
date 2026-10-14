#!/usr/bin/env bash
# Replaces the README's hero on the orphan branch `readme-assets` (D790): one
# commit holding hero.webp, force-pushed, so the 16 MB of every previous
# recording become unreachable instead of growing every clone of main.
#
# Usage: web/scripts/publish-hero.sh <file> <tag>
#   <file>  the recorded WebP (make hero writes hero.webp at the repo root)
#   <tag>   the release it shows. Its web/ tree goes in the commit as a
#           `Web-Tree:` trailer, which hero-needed.sh compares to decide whether
#           the next release must record again. A <tag> that is not a revision
#           of this repository (the bootstrap's `main@<sha>`) only labels the
#           commit, and the tree is HEAD's.
#
# Run it inside a clone, pushes to its `origin`. The commit is built with
# plumbing in the clone's own object store, not in a scratch repository, because
# the checkout's credentials (actions/checkout) belong to that clone's git dir:
# a fresh `git init` pushing to the same URL would have none.
# bash 3.2 compatible: it runs on macOS too.

set -euo pipefail

BRANCH="readme-assets"

if [ $# -ne 2 ]; then
    echo "usage: publish-hero.sh <file> <tag>" >&2
    exit 2
fi
FILE="$1"
TAG="$2"
[ -s "$FILE" ] || { echo "publish-hero: ${FILE} is missing or empty" >&2; exit 1; }

TREE="$(git rev-parse --verify --quiet "${TAG}:web" || git rev-parse --verify "HEAD:web")"

# The identity the commit is written with: the caller's, else the Actions bot.
if [ -z "${GIT_AUTHOR_NAME:-}" ] && [ -z "$(git config user.name || true)" ]; then
    export GIT_AUTHOR_NAME="github-actions[bot]" GIT_COMMITTER_NAME="github-actions[bot]"
fi
if [ -z "${GIT_AUTHOR_EMAIL:-}" ] && [ -z "$(git config user.email || true)" ]; then
    export GIT_AUTHOR_EMAIL="41898282+github-actions[bot]@users.noreply.github.com" \
        GIT_COMMITTER_EMAIL="41898282+github-actions[bot]@users.noreply.github.com"
fi

HERO_BLOB="$(git hash-object -w "$FILE")"
README_BLOB="$(printf '%s\n' \
    'The README hero lives here: written by the `hero` workflow (D790) and force-pushed on every re-recording, so this branch is always one commit. Do not commit to it.' \
    | git hash-object -w --stdin)"
ROOT="$(printf '100644 blob %s\tREADME.md\n100644 blob %s\thero.webp\n' "$README_BLOB" "$HERO_BLOB" | git mktree)"
COMMIT="$(printf 'hero: %s\n\nWeb-Tree: %s\n' "$TAG" "$TREE" | git commit-tree "$ROOT")"

git push --force origin "${COMMIT}:refs/heads/${BRANCH}"
echo "publish-hero: ${BRANCH} is ${COMMIT} (${TAG}, web tree ${TREE})"
