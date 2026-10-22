#!/usr/bin/env bash
# Prepares the review evals (cartographer-review-pr §Changing the reviewer): one detached
# worktree per case of evals/cases.md at the case's squash commit, and the
# mandate line the coordinator hands to a reviewer. It spawns nothing.
#
#   eval-setup.sh [<id>...]   all cases when no id is given; cases on one commit
#                             share one worktree named eval-<id>-<id>
#   eval-setup.sh --clean     remove every .worktrees/eval-* worktree
#
# Reads evals/cases.md only: a row is a line whose first cell is cN or kN.
# Bash 3.2 (macOS default).
set -euo pipefail

# The eval worktrees live in the main working copy's .worktrees/, even when the
# script runs from a plan worktree (git-ignored, D209: outside `make gate`).
cases="$(git rev-parse --show-toplevel)/.claude/skills/cartographer-review-pr/evals/cases.md"
root=$(cd "$(git rev-parse --git-common-dir)/.." && pwd)
cd "$root"

if [ "${1:-}" = "--clean" ]; then
  for wt in .worktrees/eval-*; do
    [ -e "$wt" ] || continue
    git worktree remove --force "$wt"
    echo "removed $wt"
  done
  git worktree prune
  exit 0
fi

# id <TAB> commit <TAB> plan, from both tables (the plan column differs).
rows=$(awk -F'|' '
  function t(s) { gsub(/^[ \t]+|[ \t]+$/, "", s); return s }
  /^\| *c[0-9]+ *\|/ { printf "%s\t%s\t%s\n", t($2), t($4), t($6) }
  /^\| *k[0-9]+ *\|/ { printf "%s\t%s\t%s\n", t($2), t($4), t($5) }
' "$cases")
[ -n "$rows" ] || { echo "no case row in $cases" >&2; exit 1; }

# One worktree per distinct commit: cases that share a commit share a run, and
# the mandate line lists every id it serves. The commit order of the file holds.
want=" ${*//,/ } "
seen=""
while IFS=$'\t' read -r id commit plan; do
  case "$seen" in *" $commit "*) continue ;; esac
  seen="$seen $commit "
  ids=""; pick=0
  while IFS=$'\t' read -r id2 commit2 _; do
    [ "$commit2" = "$commit" ] || continue
    ids="${ids:+$ids,}$id2"
    if [ $# -eq 0 ] || [[ "$want" == *" $id2 "* ]]; then pick=1; fi
  done <<<"$rows"
  [ "$pick" = 1 ] || continue
  wt=".worktrees/eval-${ids//,/-}"
  if [ ! -e "$wt" ]; then
    git worktree add --detach -q "$wt" "$commit"
  fi
  base=$(git rev-parse "$commit^")
  echo "$ids: worktree $root/$wt · base $base · plan ${plan:-none} · round 1 · eval: reviewing a past PR, so no history after HEAD (no git log, show or diff on origin/main or any ref beyond HEAD), no reading of .claude/skills/cartographer-review-pr/evals/ from any working copy, no gh lookup of a PR or issue other than the plan"
done <<<"$rows"
