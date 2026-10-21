#!/usr/bin/env bash
# triage.sh — the cheap first pass of a PR review (cartographer-review-pr skill §1).
#
#   triage.sh <pr-number>   a pull request on GitHub (needs gh, jq)
#   triage.sh --gate <pr>   the same report; exits 1 iff a BLOCK other than "CI is red"
#                           was printed (the plan-gate workflow runs this, D955)
#   triage.sh               the current branch against upstream/main (origin/main when
#                           there is no `upstream` remote), before opening a PR
#
# Prints at most ~20 lines: size split by kind, areas, review tier, linked plan,
# CI, docs and decision hints, and the BLOCK/WARN lines that send a PR back
# without anyone reading its code. It reads metadata only, never the diff body.
set -euo pipefail

# Limits are CONTRIBUTING.md §Pull requests; change them there and here together.
SIZE_CAP=500      # production lines added per PR
PLAN_FLOOR=150    # above this, a contributor's PR needs an approved plan
# Packages whose invariants are written down outside the code (an area AGENTS.md)
# or are about security and concurrency: a change there always gets a full review.
INVARIANT_AREAS='^internal/(mcpserver|provisioning|auth|gitx|sops|service)/'
# What runs on, or gets installed into, users' machines without being Go code: the
# CI workflows (they hold the release credentials), the installers, the release
# pipeline, and the skills the binary provisions into users' agents. A change there
# is a supply-chain change at any size: always a full review, never a standing merge (D857).
# The files agents execute are the same class (D955): a PR that edits AGENTS.md, a skill,
# an agent definition, the Makefile or a script steers the review and the sessions that read it.
SUPPLY_CHAIN='^(\.github/workflows/|install\.(sh|ps1)$|\.goreleaser\.yaml$|Dockerfile$|release-please-config\.json$|internal/skillbundle/bundled/|(.*/)?(AGENTS|CLAUDE)\.md$|SECURITY\.md$|CONTRIBUTING\.md$|\.agents/|\.claude/|\.kiro/|\.opencode/|\.github/agents/|\.github/ISSUE_TEMPLATE/|\.github/pull_request_template\.md$|Makefile$|scripts/)'

kind() { # production | test | docs | generated
  case "$1" in
    internal/skillbundle/bundled/*) echo production ;; # shipped prompts, not docs
    # Instructions agents execute: production and supply chain, not docs (D955).
    AGENTS.md|*/AGENTS.md|CLAUDE.md|*/CLAUDE.md|SECURITY.md|CONTRIBUTING.md) echo production ;;
    .agents/*|.claude/*|.kiro/*|.opencode/*|.github/agents/*|.github/ISSUE_TEMPLATE/*|.github/pull_request_template.md|Makefile|scripts/*) echo production ;;
    internal/webui/dist/*|docs/decisions.md|CHANGELOG.md|go.sum|web/package-lock.json) echo generated ;;
    *_test.go|*/testdata/*|test/*|web/src/test/*|web/e2e/*|*.test.ts|*.test.tsx|*.spec.ts) echo test ;;
    *.md|docs/*) echo docs ;;
    *) echo production ;;
  esac
}

GATE=0
if [ "${1:-}" = "--gate" ]; then GATE=1; shift; fi
[ "$GATE" = 0 ] || [ $# -ge 1 ] || { echo "triage.sh --gate needs a PR number" >&2; exit 2; }

# The upstream remote: `upstream` in a fork clone, else `origin` (D955).
remote=origin
git remote get-url upstream >/dev/null 2>&1 && remote=upstream

# Role (D955): the GitHub permission. author_association in a PR, the caller's
# viewerPermission before opening one. OWNER/MEMBER/COLLABORATOR and
# ADMIN/MAINTAIN/WRITE are the maintainer side; a bot is exempt from the plan rule.
role=contributor
if [ $# -ge 1 ]; then
  PR=$1
  meta=$(gh pr view "$PR" --json title,author,files,closingIssuesReferences,statusCheckRollup,body,headRefName,labels)
  files=$(jq -r '.files[] | "\(.additions)\t\(.deletions)\t\(.path)"' <<<"$meta")
  author=$(jq -r .author.login <<<"$meta")
  prinfo=$(gh api "repos/{owner}/{repo}/pulls/$PR" --jq '[.author_association, .user.type] | @tsv')
  assoc=${AUTHOR_ASSOCIATION:-${prinfo%%$'\t'*}}
  case "$assoc" in OWNER|MEMBER|COLLABORATOR) role=maintainer ;; esac
  [ "${prinfo##*$'\t'}" = Bot ] && role=bot
  echo "PR #$PR $(jq -r .title <<<"$meta") — by $author, branch $(jq -r .headRefName <<<"$meta")"
  sizelabel=$(jq -r '[.labels[].name] | index("size-exception") // empty' <<<"$meta")
  ci=$(jq -r '[.statusCheckRollup[] | (.conclusion // .state // "PENDING")] |
    if length == 0 then "none"
    elif any(. == "FAILURE" or . == "CANCELLED" or . == "TIMED_OUT" or . == "ERROR") then "FAILURE"
    elif any(. == "PENDING" or . == "IN_PROGRESS" or . == "QUEUED" or . == "") then "PENDING"
    else "SUCCESS" end' <<<"$meta")
  issues=$(jq -r '.closingIssuesReferences[].number' <<<"$meta")
  body=$(jq -r .body <<<"$meta")
else
  # Fail closed on a fork clone without `upstream`: never diff against a stale fork main.
  "$(dirname "$0")/../../../../scripts/upstream-repo.sh" --require-remote >/dev/null || exit 2
  git fetch -q "$remote" main
  files=$(git diff --numstat "$remote"/main...HEAD | awk -F'\t' '$1 != "-"')
  ci=local
  slug=$("$(dirname "$0")/../../../../scripts/upstream-repo.sh")
  author=$(gh api user --jq .login 2>/dev/null || echo me)
  sizelabel=""
  perm=$(gh repo view "$slug" --json viewerPermission --jq .viewerPermission 2>/dev/null || echo UNKNOWN)
  case "$perm" in ADMIN|MAINTAIN|WRITE) role=maintainer ;; esac
  echo "Branch $(git rev-parse --abbrev-ref HEAD) vs $remote/main (before opening a PR)"
  issues=$(git log --format=%B "$remote"/main..HEAD | grep -oiE '(closes|fixes) #[0-9]+' | grep -oE '[0-9]+' | sort -u || true)
  body=""
fi
echo "Role: $role"

# Plain bash 3.2 (the macOS default): no associative arrays.
classified=$(while IFS=$'\t' read -r a d path; do
  [ -n "$path" ] && printf '%s\t%s\t%s\t%s\n' "$(kind "$path")" "$a" "$d" "$path"
done <<<"$files")
sum() { awk -F'\t' -v k="$1" -v c="$2" '$1 == k { s += $c } END { print s + 0 }' <<<"$classified"; }
paths_of() { awk -F'\t' -v k="$1" '$1 == k { print $4 }' <<<"$classified"; }

prod=$(sum production 2)
echo "Size: production +$prod/-$(sum production 3) (cap $SIZE_CAP) · tests +$(sum test 2) · docs +$(sum docs 2) · generated +$(sum generated 2)"
echo "Areas: $(cut -f4 <<<"$classified" | cut -d/ -f1-2 | sort | uniq -c | sort -rn | awk 'NF { printf "%s(%s) ", $2, $1 }')"
docs_touched=$(cut -f4 <<<"$classified" | grep -m1 '^docs/' || true)
decision=$(cut -f4 <<<"$classified" | grep -m1 '^docs/decisions/D' || true)

invariant=$(paths_of production | grep -E "$INVARIANT_AREAS" | cut -d/ -f1-2 | sort -u || true)
supply=$(paths_of production | grep -E "$SUPPLY_CHAIN" || true)
if [ -n "$invariant" ] || [ -n "$supply" ]; then
  tier=full
elif [ "$prod" -le 50 ]; then
  tier=light
else
  tier=standard
fi
echo "Tier: $tier${invariant:+ — invariant areas: $(tr '\n' ' ' <<<"$invariant")}${supply:+ — supply chain: $(tr '\n' ' ' <<<"$supply")}"
[ -n "$supply" ] && echo "WARN: supply-chain files changed — the user reads those hunks and decides the merge; no standing approval covers it"

# The next train is the open milestone due first (CONTRIBUTING.md §Releases): only its
# work merges, the rest waits in its open PR until the train before it has left.
next=$(gh api "repos/{owner}/{repo}/milestones?state=open&sort=due_on&direction=asc" --jq '.[0].title // empty' 2>/dev/null || true)
plan=""; plan_ok=""; unassigned=""; later=""
for n in $issues; do
  # Split on "|": a label may hold spaces (`good first issue`), and a tab, being
  # whitespace to read, would swallow an empty label list.
  IFS='|' read -r labels milestone < <(gh issue view "$n" --json labels,milestone \
    --jq '"\([.labels[].name] | join(","))|\(.milestone.title // "-")"' 2>/dev/null || echo '?|?')
  echo "Closes #$n [$labels] milestone ${milestone/#-/none}"
  if [ "$milestone" = - ]; then
    echo "WARN: #$n has no milestone — it is not triaged yet: the maintainer puts it in a train or closes it"
  elif [ -n "$next" ] && [ "$milestone" != "$next" ] && [ "$milestone" != "?" ]; then
    later="$later #$n ($milestone)"
  fi
  if [[ ",$labels," == *",plan,"* ]]; then
    plan=$n
    if [[ ",$labels," == *",approved,"* ]]; then
      # A contributor claims an approved plan by commenting on it; the maintainer assigns it (D955).
      assignees=$(gh issue view "$n" --json assignees --jq '[.assignees[].login] | join(",")' 2>/dev/null || echo "")
      if [ "$role" != contributor ] || [[ ",$assignees," == *",$author,"* ]]; then
        plan_ok=$n
      else
        unassigned=$n
      fi
    fi
  fi
done
[ -n "$issues" ] || echo "Closes: nothing"
echo "CI: $ci"

# Counted for --gate: every BLOCK but the red CI, which must not feed back into the check itself.
blocked=0
[ "$ci" = FAILURE ] && echo "BLOCK: CI is red — nothing to review until it is green"
if [ "$role" = contributor ] && [ "$prod" -gt "$PLAN_FLOOR" ] && [ -z "$plan_ok" ]; then
  blocked=1
  if [ -n "$unassigned" ]; then
    echo "BLOCK: plan #$unassigned is approved but not assigned to $author: comment on it to claim it, and ask the maintainer to assign the plan to you"
  else
    echo "BLOCK: $prod production lines from a contributor without an approved plan (an issue labelled plan + approved): the design is reviewed there first"
  fi
fi
if [ -n "$later" ] && [ "$role" != bot ]; then
  blocked=1
  echo "BLOCK: closes work of a later train:$later; the next train is $next — the PR waits, open, until the train before it has left"
fi
if [ "$prod" -gt "$SIZE_CAP" ]; then
  # A contributor's own body line would let any PR through the required check: for a
  # contributor the exception is the maintainer-set `size-exception` label (D955).
  if [ -n "$sizelabel" ] || { [ "$role" != contributor ] && grep -qi '^size exception:' <<<"$body"; }; then
    echo "WARN: over the size cap, with an accepted exception ($([ -n "$sizelabel" ] && echo "label size-exception" || grep -i -m1 '^size exception:' <<<"$body"))"
  else
    blocked=1
    echo "BLOCK: over the size cap ($prod > $SIZE_CAP production lines) — split it by work package"
  fi
fi
if [ -z "$docs_touched" ] && [ "$prod" -gt 50 ]; then
  echo "WARN: production code changed and no docs/ page touched — check docs/index.md §Documentation maintenance rules"
fi
[ -n "$decision" ] && echo "Decision file: $decision"
regression=$(grep -i -m1 '^regression-of:' <<<"$body" || true)
[ -n "$regression" ] && echo "$regression — a regression: if it merges, add it to .agents/skills/cartographer-review-pr/evals/cases.md (cartographer-review-pr §When a defect escapes)"
if [ $# -ge 1 ]; then
  for section in '## Self-review' '## Docs' '## Revert'; do
    grep -qF "$section" <<<"$body" || echo "WARN: PR body has no \"$section\" section (.github/pull_request_template.md)"
  done
fi
[ "$GATE" = 1 ] && [ "$blocked" = 1 ] && exit 1
exit 0
