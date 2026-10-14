#!/usr/bin/env bash
# Whether a release must record the README hero again (D790): exits 0 when it
# must, 1 when the hero on `readme-assets` already shows this release's web/
# tree, and says why on stdout. Any other exit is a failure of the check itself.
#
# The judge is the tree hash of web/ (the UI, the tour and the recording scripts
# are all under it), kept in the `Web-Tree:` trailer publish-hero.sh writes. It
# does not depend on which tag came before. No branch or no trailer: record.
#
# Usage: web/scripts/hero-needed.sh <tag>   (inside a clone with an `origin`)
# bash 3.2 compatible: it runs on macOS too.

set -euo pipefail

BRANCH="readme-assets"

if [ $# -ne 1 ]; then
    echo "usage: hero-needed.sh <tag>" >&2
    exit 2
fi
TAG="$1"

WANT="$(git rev-parse --verify "${TAG}:web")" || { echo "hero-needed: ${TAG} has no web/ tree" >&2; exit 2; }

# 2 is ls-remote's "no such ref"; any other failure is the network or the URL.
rc=0
git ls-remote --exit-code --heads origin "$BRANCH" >/dev/null 2>&1 || rc=$?
if [ "$rc" -eq 2 ]; then
    echo "needed: no ${BRANCH} branch yet"
    exit 0
elif [ "$rc" -ne 0 ]; then
    echo "hero-needed: cannot reach origin (ls-remote exit ${rc})" >&2
    exit 3
fi

git fetch --quiet --depth 1 origin "refs/heads/${BRANCH}"
HAVE="$(git log -1 --format=%B FETCH_HEAD | sed -n 's/^Web-Tree: *//p' | head -n 1)"
if [ -z "$HAVE" ]; then
    echo "needed: ${BRANCH} carries no Web-Tree trailer"
    exit 0
fi
if [ "$HAVE" = "$WANT" ]; then
    echo "not needed: ${BRANCH} already shows web tree ${WANT} (${TAG})"
    exit 1
fi
echo "needed: web tree ${WANT} (${TAG}) differs from ${HAVE} on ${BRANCH}"
exit 0
