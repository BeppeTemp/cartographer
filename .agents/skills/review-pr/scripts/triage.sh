#!/usr/bin/env bash
# triage.sh — the cheap first pass of a PR review (review-pr skill §1).
#
#   triage.sh <pr-number>   a pull request on GitHub (needs gh, jq)
#   triage.sh               the current branch against origin/main, before opening one
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

kind() { # production | test | docs | generated
  case "$1" in
    internal/webui/dist/*|docs/decisions.md|CHANGELOG.md|go.sum|web/package-lock.json) echo generated ;;
    *_test.go|*/testdata/*|test/*|web/src/test/*|web/e2e/*|*.test.ts|*.test.tsx|*.spec.ts) echo test ;;
    *.md|docs/*) echo docs ;;
    *) echo production ;;
  esac
}

if [ $# -ge 1 ]; then
  PR=$1
  meta=$(gh pr view "$PR" --json title,author,files,closingIssuesReferences,statusCheckRollup,body,headRefName)
  files=$(jq -r '.files[] | "\(.additions)\t\(.deletions)\t\(.path)"' <<<"$meta")
  author=$(jq -r .author.login <<<"$meta")
  owner=$(gh repo view --json owner --jq .owner.login)
  echo "PR #$PR $(jq -r .title <<<"$meta") — by $author, branch $(jq -r .headRefName <<<"$meta")"
  ci=$(jq -r '[.statusCheckRollup[] | (.conclusion // .state // "PENDING")] |
    if length == 0 then "none"
    elif any(. == "FAILURE" or . == "CANCELLED" or . == "TIMED_OUT" or . == "ERROR") then "FAILURE"
    elif any(. == "PENDING" or . == "IN_PROGRESS" or . == "QUEUED" or . == "") then "PENDING"
    else "SUCCESS" end' <<<"$meta")
  issues=$(jq -r '.closingIssuesReferences[].number' <<<"$meta")
  body=$(jq -r .body <<<"$meta")
else
  git fetch -q origin main
  files=$(git diff --numstat origin/main...HEAD | awk -F'\t' '$1 != "-"')
  author=me; owner=me; ci=local
  echo "Branch $(git rev-parse --abbrev-ref HEAD) vs origin/main (before opening a PR)"
  issues=$(git log --format=%B origin/main..HEAD | grep -oiE '(closes|fixes) #[0-9]+' | grep -oE '[0-9]+' | sort -u || true)
  body=""
fi

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
if [ -n "$invariant" ]; then
  tier=full
elif [ "$prod" -le 50 ]; then
  tier=light
else
  tier=standard
fi
echo "Tier: $tier${invariant:+ — invariant areas: $(tr '\n' ' ' <<<"$invariant")}"

plan=""; plan_ok=""
for n in $issues; do
  labels=$(gh issue view "$n" --json labels --jq '[.labels[].name] | join(",")' 2>/dev/null || echo "?")
  echo "Closes #$n [$labels]"
  if [[ ",$labels," == *",plan,"* ]]; then
    plan=$n
    [[ ",$labels," == *",approved,"* ]] && plan_ok=$n
  fi
done
[ -n "$issues" ] || echo "Closes: nothing"
echo "CI: $ci"

[ "$ci" = FAILURE ] && echo "BLOCK: CI is red — nothing to review until it is green"
if [ "$author" != "$owner" ] && [ "$prod" -gt "$PLAN_FLOOR" ] && [ -z "$plan_ok" ]; then
  echo "BLOCK: $prod production lines from a contributor without an approved plan (an issue labelled plan + approved): the design is reviewed there first"
fi
if [ "$prod" -gt "$SIZE_CAP" ]; then
  if grep -qi '^size exception:' <<<"$body"; then
    echo "WARN: over the size cap, with a stated exception: $(grep -i -m1 '^size exception:' <<<"$body")"
  else
    echo "BLOCK: over the size cap ($prod > $SIZE_CAP production lines) — split it by work package"
  fi
fi
if [ -z "$docs_touched" ] && [ "$prod" -gt 50 ]; then
  echo "WARN: production code changed and no docs/ page touched — check docs/index.md §Documentation maintenance rules"
fi
[ -n "$decision" ] && echo "Decision file: $decision"
if [ $# -ge 1 ]; then
  for section in '## Self-review' '## Docs'; do
    grep -qF "$section" <<<"$body" || echo "WARN: PR body has no \"$section\" section (.github/pull_request_template.md)"
  done
fi
