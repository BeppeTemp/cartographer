#!/usr/bin/env bash
# release-as.sh — does the release PR need a Release-As commit? (D1371)
#
# Usage: release-as.sh <release-pr-title> <milestone-title>
#
# The next train is named by its milestone (v<x.y.z>), but release-please
# computes the version from the commits (bump-minor-pre-major): after a run of
# fix: commits it proposes x.y.(z+1) for a train named x.(y+1).0. This script is
# the one place that compares the two; .github/workflows/release-as.yml only
# does the API calls.
#
# One line on stdout, exit 0:
#   act <version>             the PR version is lower than the milestone's
#   same                      equal: nothing to do
#   higher <pr> <milestone>   the PR is ahead (a breaking change): reported, never acted on
#   skip <reason>             the milestone title is not v<x.y.z>
# Exit 1 with a message on stderr when the PR title carries no version.
set -euo pipefail

[ $# -eq 2 ] || { echo "usage: release-as.sh <release-pr-title> <milestone-title>" >&2; exit 2; }
title=$1
milestone=$2

# Same expression as release-train.yml (the Wednesday step).
pr=$(printf '%s' "$title" | sed -n 's/^chore([a-z]*): release \([0-9][0-9.]*[0-9]\).*/\1/p')
if ! printf '%s' "$pr" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "release-as.sh: cannot read a version X.Y.Z from the release PR title '$title'" >&2
  exit 1
fi
if ! printf '%s' "$milestone" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "skip milestone '$milestone' is not v<x.y.z>"
  exit 0
fi
want=${milestone#v}

# Numeric, field by field: 0.9.0 < 0.28.0.
IFS=. read -r pa pb pc <<<"$pr"
IFS=. read -r wa wb wc <<<"$want"
cmp=0
for pair in "$pa $wa" "$pb $wb" "$pc $wc"; do
  set -- $pair
  if [ $((10#$1)) -lt $((10#$2)) ]; then cmp=-1; break; fi
  if [ $((10#$1)) -gt $((10#$2)) ]; then cmp=1; break; fi
done
case $cmp in
  -1) echo "act $want" ;;
  0) echo "same" ;;
  *) echo "higher $pr $want" ;;
esac
