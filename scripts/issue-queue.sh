#!/usr/bin/env bash
# issue-queue.sh — the open issues as a work queue: class, state, next action (make issues).
#
#   make issues
#   issue-queue.sh --tsv   the same issues in the same order, one row each for a script
#                          (scripts/status.sh): number, class, state, age in hours, `late` or `-`,
#                          `blocks-rc` (the Found-by rule below, not the failed ref) or `-`,
#                          the Found-by values, title; no `parked` line
#
# Read-only, like scripts/status.sh: it never edits an issue, pushes or writes a ref on origin.
# One line per open issue, `#<n> <class> <state>: <next action> [<age>] — <title>`, the fixable ones
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
# The class is the issue's kind: ci-red, nightly-red, export, bug, or `other`. `export` is an
# `idea` issue: ideas are not accepted work on GitHub, the maintainer records each in the backlog
# and closes it (CONTRIBUTING.md §Issues), so its state is `open` unless one of the first four holds.
# The age is the time since the issue was opened (`<n>h`, `<n>d` from two days), followed by
# `late: over <n>h` past the threshold of its class, the first that holds: the state
# needs-maintainer 24 h, ci-red or flaky 2 h, a bug with `Found-by: rc` 8 h, any other bug 24 h; the
# other classes have none. `blocks the candidate` marks what makes `release-train` with `action: rc`
# skip: an issue labelled `bug` and `accepted`, whatever its class here, whose body, written by a maintainer or github-actions[bot], has a `Found-by: rc`
# or `Found-by: nightly` line, or a `failed/fix-<n>` ref (the `found_by` and `trusted` lines below are
# the ones of .github/workflows/release-train.yml; release_train_test.go pins that they match).
# ISSUE_QUEUE_NOW=<epoch> is the clock, for a test. Each `wip/<slug>`
# branch on origin (a change parked, not queued: scripts/unlanded.sh) is a line of class `parked`,
# printed first because the release candidate refuses while one exists:
# `wip/<slug> parked parked: <next action> — <subject of its tip>`. Exit 0 with the
# list, 1 when GitHub cannot be read. The skill cartographer-fix-issues works through it.
# internal/repodocs/landertest/issue_loop_test.go pins it. Plain bash 3.2.
set -uo pipefail
export LC_ALL=C

here=$(cd "$(dirname "$0")" && pwd)
top=$(git rev-parse --show-toplevel) || exit 1
cd "$top" || exit 1
tsv=
case "${1:-}" in "") ;; --tsv) tsv=1 ;; *) echo "usage: issue-queue.sh [--tsv]" >&2; exit 2 ;; esac
now=${ISSUE_QUEUE_NOW:-$(date +%s)}
[[ "$now" =~ ^[0-9]+$ ]] || { echo "issues: ISSUE_QUEUE_NOW must be an epoch" >&2; exit 2; }
repo=$("$here/upstream-repo.sh" 2>/dev/null) || { echo "issues: the upstream repository cannot be read" >&2; exit 1; }
git fetch -q --no-tags --prune origin '+refs/heads/ready/*:refs/remotes/origin/ready/*' \
  '+refs/heads/failed/*:refs/remotes/origin/failed/*' '+refs/heads/wip/*:refs/remotes/origin/wip/*' >/dev/null 2>&1 || true
# The REST list, not `gh issue list`: only it gives the author association. Pull requests are dropped.
# No field is ever empty: `read` with a tab IFS folds two tabs into one.
list=$(gh api --paginate "repos/$repo/issues?state=open&per_page=100" --jq '
  def found_by: [(.body // "") | split("\n")[] | rtrimstr("\r") | select(test("^Found-by: [a-z-]+$")) | ltrimstr("Found-by: ")];
  def trusted: .user.login == "github-actions[bot]" or (.author_association | IN("OWNER", "MEMBER", "COLLABORATOR"));
  .[] | select(.pull_request | not) | [(.number | tostring), ([.labels[].name] | join(",") | if . == "" then "-" else . end), (.assignees | length | tostring),
    (if trusted then "trusted" else "external" end),
    ((.created_at // "") | try (('"$now"' - fromdateiso8601) / 3600 | floor | if . < 0 then 0 else . end | tostring) catch "-"),
    (if trusted then (found_by | join(",")) else "" end | if . == "" then "-" else . end),
    (.title | gsub("[\t\n\r]"; " "))] | @tsv' 2>/dev/null) ||
  { echo "issues: GitHub cannot be read" >&2; exit 1; }
refs=$(git for-each-ref --format='%(refname)' refs/remotes/origin/ready refs/remotes/origin/failed)
# The main worktree's .worktrees, wherever this runs from.
wt=$(cd "$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")" && pwd)/.worktrees

parked=$(git for-each-ref --format='%(refname:lstrip=3)%09%(contents:subject)' refs/remotes/origin/wip |
  awk -F'\t' '{ gsub(/[[:cntrl:]]/, "", $2); printf "%s parked parked: make ready it or move it out (its needs-maintainer issue says what blocks it) — %s\n", $1, $2 }')
[ -z "$parked" ] || [ -n "$tsv" ] || printf '%s\n' "$parked"
[ -n "$list" ] || { [ -n "$parked" ] || [ -n "$tsv" ] || echo "issues: none open"; exit 0; }
while IFS=$'\t' read -r n labels assigned author age fb title; do
  [[ "$n" =~ ^[0-9]+$ ]] || continue
  has() { [[ ",$labels," == *",$1,"* ]]; }
  class=other
  for c in ci-red nightly-red idea bug; do has "$c" && { class=$c; break; }; done
  [ "$class" != idea ] || class="export"
  if has needs-maintainer; then state=needs-maintainer next="the maintainer answers the question on the issue"
  elif grep -qxF "refs/remotes/origin/ready/fix-$n" <<<"$refs"; then state=queued next="the lander lands it"
  elif grep -qxF "refs/remotes/origin/failed/fix-$n" <<<"$refs"; then state=rejected next="make requeue SLUG=fix-$n after a fix round"
  elif [ "$assigned" != 0 ] || [ -d "$wt/fix-$n" ]; then state=fixing next="the session that took it"
  elif [ "$class" = export ]; then state=open next="the maintainer records it in the backlog, then closes it"
  elif ! has accepted && [ "$class" != ci-red ] && [ "$class" != nightly-red ]; then
    state=untriaged next="triage: reproduce it, then accept or close"
  elif [ "$class" = bug ] && [ "$author" != trusted ]; then
    state=external next="the maintainer: a design comment, then an explicit make ready (outside the standing approval)"
  else
    state=idle
    case "$class" in bug | ci-red | nightly-red) next="fix it (cartographer-fix-issues)" ;; *) next="triage" ;; esac
  fi
  rank=3
  if [ "$state" = idle ]; then
    rank=2
    { [ "$class" = ci-red ] || [[ "$title" == flaky* ]]; } && rank=1
  fi
  limit=
  if [ "$state" = needs-maintainer ]; then limit=24
  elif [ "$class" = ci-red ] || { [ "$class" = bug ] && [[ "$title" == flaky* ]]; }; then limit=2
  elif [ "$class" = bug ] && [[ ",$fb," == *,rc,* ]]; then limit=8
  elif [ "$class" = bug ]; then limit=24
  fi
  late=-
  [ -z "$limit" ] || [ "$age" = - ] || [ "$age" -le "$limit" ] || late=late
  blocks=-
  if has bug && has accepted && [[ ",$fb," == *,rc,* || ",$fb," == *,nightly,* ]]; then blocks="blocks-rc"; fi
  if [ -n "$tsv" ]; then
    printf '%s\t%08d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$rank" "$n" "$n" "$class" "$state" "$age" "$late" "$blocks" "$fb" "$title"
    continue
  fi
  tag=
  if [ "$age" != - ]; then
    if [ "$age" -ge 48 ]; then tag="$((age / 24))d"; else tag="${age}h"; fi
    [ "$late" = - ] || tag="$tag, late: over ${limit}h"
  fi
  { [ "$blocks" = - ] && [ "$state" != rejected ]; } || tag="${tag:+$tag, }blocks the candidate"
  printf '%s\t%08d\t#%s %s %s: %s%s — %s\n' "$rank" "$n" "$n" "$class" "$state" "$next" "${tag:+ [$tag]}" "$title"
done <<<"$list" | sort -t$'\t' -k1,1n -k2,2 | cut -f3-
exit 0
