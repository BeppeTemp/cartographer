#!/usr/bin/env bash
# issue-queue.sh — the open issues as a work queue: class, state, next action (make issues).
#
#   make issues
#
# Read-only, like scripts/status.sh: it never edits an issue, pushes or writes a ref on origin.
# One line per open issue, `#<n> <class> <state>: <next action> — <title>`, the fixable ones
# first: idle `ci-red` and flaky (a title starting `flaky`) bugs, then the other idle bugs, oldest
# first within each, then the rest by number. The state comes from signals that already exist
# (CONTRIBUTING.md §Fixes without a plan), the first that holds:
#   needs-maintainer  the label: a question waits for the maintainer
#   queued            ready/fix-<n> on origin
#   rejected          failed/fix-<n> on origin
#   fixing            an assignee, or a local worktree .worktrees/fix-<n>
#   external          an accepted bug opened by anyone other than a maintainer (author association
#                     OWNER, MEMBER or COLLABORATOR) or github-actions[bot]: it is outside the standing
#                     approval, it needs a maintainer's design comment and an explicit `make ready`
#                     from the maintainer (CONTRIBUTING.md §Fixes without a plan)
#   untriaged         no `accepted` label (a `ci-red` or `nightly-red` issue is accepted by its workflow)
#   idle              accepted, nobody on it
# The class is the issue's kind: ci-red, nightly-red, idea, bug, or `other`. Exit 0 with the
# list, 1 when GitHub cannot be read. The skill cartographer-fix-issues works through it.
# internal/repodocs/landertest/issue_loop_test.go pins it. Plain bash 3.2.
set -uo pipefail
export LC_ALL=C

here=$(cd "$(dirname "$0")" && pwd)
top=$(git rev-parse --show-toplevel) || exit 1
cd "$top" || exit 1
repo=$("$here/upstream-repo.sh" 2>/dev/null) || { echo "issues: the upstream repository cannot be read" >&2; exit 1; }
git fetch -q --no-tags --prune origin '+refs/heads/ready/*:refs/remotes/origin/ready/*' \
  '+refs/heads/failed/*:refs/remotes/origin/failed/*' >/dev/null 2>&1 || true
# The REST list, not `gh issue list`: only it gives the author association. Pull requests are dropped.
list=$(gh api --paginate "repos/$repo/issues?state=open&per_page=100" \
  --jq '.[] | select(.pull_request | not) | [(.number | tostring), ([.labels[].name] | join(",")), (.assignees | length | tostring),
    (if .user.login == "github-actions[bot]" or (.author_association | IN("OWNER", "MEMBER", "COLLABORATOR")) then "trusted" else "external" end),
    (.title | gsub("[\t\n\r]"; " "))] | @tsv' 2>/dev/null) ||
  { echo "issues: GitHub cannot be read" >&2; exit 1; }
refs=$(git for-each-ref --format='%(refname)' refs/remotes/origin/ready refs/remotes/origin/failed)
# The main worktree's .worktrees, wherever this runs from.
wt=$(cd "$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")" && pwd)/.worktrees

[ -n "$list" ] || { echo "issues: none open"; exit 0; }
while IFS=$'\t' read -r n labels assigned author title; do
  [[ "$n" =~ ^[0-9]+$ ]] || continue
  has() { [[ ",$labels," == *",$1,"* ]]; }
  class=other
  for c in ci-red nightly-red idea bug; do has "$c" && { class=$c; break; }; done
  if has needs-maintainer; then state=needs-maintainer next="the maintainer answers the question on the issue"
  elif grep -qxF "refs/remotes/origin/ready/fix-$n" <<<"$refs"; then state=queued next="the lander lands it"
  elif grep -qxF "refs/remotes/origin/failed/fix-$n" <<<"$refs"; then state=rejected next="make requeue SLUG=fix-$n after a fix round"
  elif [ "$assigned" != 0 ] || [ -d "$wt/fix-$n" ]; then state=fixing next="the session that took it"
  elif ! has accepted && [ "$class" != ci-red ] && [ "$class" != nightly-red ]; then
    state=untriaged
    case "$class" in idea) next="the maintainer accepts, parks or closes it" ;; *) next="triage: reproduce it, then accept or close" ;; esac
  elif [ "$class" = bug ] && [ "$author" != trusted ]; then
    state=external next="the maintainer: a design comment, then an explicit make ready (outside the standing approval)"
  else
    state=idle
    case "$class" in idea) next="a plan (cartographer-propose-plan)" ;; bug | ci-red | nightly-red) next="fix it (cartographer-fix-issues)" ;; *) next="triage" ;; esac
  fi
  rank=3
  if [ "$state" = idle ] && [ "$class" != idea ]; then
    rank=2
    { [ "$class" = ci-red ] || [[ "$title" == flaky* ]]; } && rank=1
  fi
  printf '%s\t%08d\t#%s %s %s: %s — %s\n' "$rank" "$n" "$n" "$class" "$state" "$next" "$title"
done <<<"$list" | sort -t$'\t' -k1,1n -k2,2 | cut -f3-
exit 0
