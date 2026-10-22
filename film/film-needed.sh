#!/usr/bin/env bash
# Whether the README film must be rendered again (D1068): exits 0 when it must, 1 when the film on
# `readme-film` already shows these sources, and says why on stdout. Exit 2 is a usage error or a bad
# revision (or an input missing at it), 3 an unreachable origin.
#
# The judge is the hash of the film's inputs (film/inputs.sh), kept in the `Film-Tree:` trailer film/publish.sh
# writes. No branch or no trailer: render. Modelled on web/scripts/hero-needed.sh.
#
# Usage: film/film-needed.sh <rev>   (inside a clone with an `origin`)
# bash 3.2 compatible: it runs on macOS too.

set -euo pipefail
# shellcheck source=film/inputs.sh
. "$(dirname "${BASH_SOURCE[0]}")/inputs.sh"

BRANCH="readme-film"

if [ $# -ne 1 ]; then
    echo "usage: film-needed.sh <rev>" >&2
    exit 2
fi
REV="$1"

git rev-parse --verify --quiet "${REV}^{commit}" >/dev/null || { echo "film-needed: ${REV} is not a revision" >&2; exit 2; }
WANT="$(film_tree "$REV")" || exit 2

# 2 is ls-remote's "no such ref"; any other failure is the network or the URL.
rc=0
git ls-remote --exit-code --heads origin "$BRANCH" >/dev/null 2>&1 || rc=$?
if [ "$rc" -eq 2 ]; then
    echo "needed: no ${BRANCH} branch yet"
    exit 0
elif [ "$rc" -ne 0 ]; then
    echo "film-needed: cannot reach origin (ls-remote exit ${rc})" >&2
    exit 3
fi

git fetch --quiet --depth 1 origin "refs/heads/${BRANCH}"
HAVE="$(git log -1 --format=%B FETCH_HEAD | sed -n 's/^Film-Tree: *//p' | head -n 1)"
if [ -z "$HAVE" ]; then
    echo "needed: ${BRANCH} carries no Film-Tree trailer"
    exit 0
fi
if [ "$HAVE" = "$WANT" ]; then
    echo "not needed: ${BRANCH} already shows film tree ${WANT} (${REV})"
    exit 1
fi
echo "needed: film tree ${WANT} (${REV}) differs from ${HAVE} on ${BRANCH}"
exit 0
