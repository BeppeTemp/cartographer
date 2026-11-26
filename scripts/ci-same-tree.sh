#!/usr/bin/env bash
# ci-same-tree.sh — on a push to main, did a green dispatched ci.yml run already test this tree?
# (ci.yml's `same-tree` job, D2415)
#
# Prints `green=true` and `run=<id>` when one of the last SAME_TREE_RUNS (20) successful
# workflow_dispatch runs of ci.yml ran on a commit whose tree is SHA's: the hosted train gate
# dispatches ci.yml on the train branch, and a train merges by rebase only onto the main it was
# gated on, so its commits on main carry that tree. Prints `green=false` otherwise, and on any
# read that fails: it fails toward running every leg, and always exits 0.
#
# Environment: REPO (owner/name), SHA (the pushed commit), GH_TOKEN (actions: read, contents: read).
# internal/repodocs/ci_same_tree_test.go pins it. Plain bash 3.2.
set -uo pipefail

no() { echo "green=false"; exit 0; }
tree_of() { gh api "repos/$REPO/git/commits/$1" --jq .tree.sha 2>/dev/null; }

[[ "${SHA:-}" =~ ^[0-9a-f]{40}$ ]] && [ -n "${REPO:-}" ] || no
want=$(tree_of "$SHA") && [[ "$want" =~ ^[0-9a-f]{40}$ ]] || no
runs=$(gh run list -R "$REPO" --workflow ci.yml --event workflow_dispatch --status success \
  --limit "${SAME_TREE_RUNS:-20}" --json databaseId,headSha --jq '.[] | "\(.databaseId) \(.headSha)"' 2>/dev/null) || no
while read -r id head; do
  [[ "$id" =~ ^[0-9]+$ && "$head" =~ ^[0-9a-f]{40}$ ]] || continue
  if [ "$(tree_of "$head")" = "$want" ]; then
    echo "green=true"
    echo "run=$id"
    echo "ci-same-tree: ci.yml run $id tested this tree on $head: the heavy legs skip" >&2
    exit 0
  fi
done <<<"$runs"
no
