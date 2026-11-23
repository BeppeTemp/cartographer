#!/usr/bin/env bash
# pr-triage.sh — the cheap first pass of a PR review (cartographer-review-pr skill §1).
#
#   pr-triage.sh <pr-number>   a pull request on GitHub (needs gh, jq)
#   pr-triage.sh --gate <pr>   the same report; exits 1 iff a BLOCK other than "CI is red"
#                           was printed (the plan-gate workflow runs this, D955)
#   pr-triage.sh --title-scopes  the scopes a PR title may use, as pr-title.yml lists them
#   pr-triage.sh               the current branch against upstream/main (origin/main when
#                           there is no `upstream` remote), before opening a PR; TRIAGE_BODY
#                           is the body it will carry (scripts/ready.sh passes the message),
#                           TRIAGE_BASE the commit it is measured from (default: the merge-base)
#
# Prints at most ~20 lines: size split by kind, areas, review tier, closed issues,
# CI, docs and decision hints (the docs-owner WARN comes from doc-owners.sh), in the branch
# mode the acceptance criteria line (ac-trace.sh), and the BLOCK/WARN lines that send a PR
# back without anyone reading its code. It reads metadata, never the diff body; the branch
# mode also reads the tree at HEAD, for the tests that cite a criterion.
set -euo pipefail

# Limits are CONTRIBUTING.md §Pull requests; change them there and here together.
SIZE_CAP=500      # production lines added per PR
# What a kind of change owes (its review tier floor, its required trailers, whether it is held
# in the freeze, whether a standing approval may queue it) is data: scripts/profiles.tsv, read
# below, where the invariant areas and the supply-chain files are rows.
PROFILES=$(dirname "$0")/profiles.tsv

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

# The scopes `pr-title` accepts: the `scopes: |` block of its workflow, the only list
# there is (a test reads the same one); never copy it here.
title_scopes() {
  awk '
    /^[[:space:]]*scopes: \|[[:space:]]*$/ { match($0, /^ */); ind = RLENGTH; on = 1; next }
    on { match($0, /^ */); if ($0 ~ /^[[:space:]]*$/ || RLENGTH <= ind) exit; gsub(/^[[:space:]]+|[[:space:]]+$/, ""); print }
  ' "$(dirname "$0")/../.github/workflows/pr-title.yml"
}
if [ "${1:-}" = "--title-scopes" ]; then title_scopes; exit 0; fi

GATE=0
if [ "${1:-}" = "--gate" ]; then GATE=1; shift; fi
[ "$GATE" = 0 ] || [ $# -ge 1 ] || { echo "pr-triage.sh --gate needs a PR number" >&2; exit 2; }

# gh_retry <gh args>: gh, asked again with a short backoff when GitHub answers 5xx (its diff of
# a just-opened PR is not always computed yet: `HTTP 500: this diff is temporarily unavailable`).
# Any other failure ends at once. PR_TRIAGE_RETRY_SLEEP (10 s) is the first backoff, doubled.
gh_retry() {
  local try err out rc nap=${PR_TRIAGE_RETRY_SLEEP:-10}
  err=$(mktemp) out=$(mktemp)
  for try in 1 2 3 4 5; do
    # The output of a try is kept apart: `--paginate` prints page 1 before page 2 answers 5xx,
    # and a retry would append the whole list again.
    rc=0; gh "$@" >"$out" 2>"$err" || rc=$?
    if [ "$rc" = 0 ]; then cat "$out"; rm -f "$err" "$out"; return 0; fi
    if [ "$try" = 5 ] || ! grep -qE 'HTTP 5[0-9][0-9]' "$err"; then cat "$err" >&2; rm -f "$err" "$out"; return "$rc"; fi
    echo "pr-triage: GitHub answered 5xx, asking again in ${nap}s" >&2
    sleep "$nap"; nap=$((nap * 2))
  done
}

# The upstream remote: `upstream` in a fork clone, else `origin` (D955).
remote=origin
git remote get-url upstream >/dev/null 2>&1 && remote=upstream

# Role (D955): the GitHub permission. author_association in a PR, the caller's
# viewerPermission before opening one. OWNER and ADMIN/MAINTAIN/WRITE are the
# maintainer side; a MEMBER or a COLLABORATOR is one only when the user's own
# permission on the repository is write or higher (a read or triage one is a
# contributor); a bot has its own role.
role=contributor
# Set in the branch mode when the permission could not be read: the eval check fails closed on it.
perm_unread=
if [ $# -ge 1 ]; then
  PR=$1
  meta=$(gh pr view "$PR" --json title,author,closingIssuesReferences,statusCheckRollup,body,headRefName,labels)
  # Not `gh pr view --json files`: it stops at 100 files, and every count below
  # (size, tier, supply chain, docs owners) would be taken on a truncated list.
  # `@tsv` escapes a tab, a newline or a backslash in a file name, so one file is one
  # row of three fields whatever its name holds; a string template would not.
  files=$(gh_retry api --paginate "repos/{owner}/{repo}/pulls/$PR/files" --jq '.[] | [.additions, .deletions, .filename] | @tsv')
  author=$(jq -r .author.login <<<"$meta")
  prinfo=$(gh_retry api "repos/{owner}/{repo}/pulls/$PR" --jq '[.author_association, .user.type] | @tsv')
  assoc=${AUTHOR_ASSOCIATION:-${prinfo%%$'\t'*}}
  case "$assoc" in
    OWNER) role=maintainer ;;
    MEMBER|COLLABORATOR)
      # An unreadable answer leaves the contributor role.
      collab=$(gh api "repos/{owner}/{repo}/collaborators/$author/permission" --jq .permission 2>/dev/null || true)
      case "$collab" in admin|maintain|write) role=maintainer ;; esac ;;
  esac
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
  title=$(jq -r .title <<<"$meta")
else
  # Fail closed on a fork clone without `upstream`: never diff against a stale fork main.
  "$(dirname "$0")/upstream-repo.sh" --require-remote >/dev/null || exit 2
  git fetch -q "$remote" main
  tbase=${TRIAGE_BASE:-$(git merge-base "$remote"/main HEAD)}
  files=$(git diff --numstat "$tbase" HEAD | awk -F'\t' '$1 != "-"')
  ci=local
  slug=$("$(dirname "$0")/upstream-repo.sh")
  author=$(gh api user --jq .login 2>/dev/null || echo me)
  sizelabel=""
  perm=$(gh repo view "$slug" --json viewerPermission --jq .viewerPermission 2>/dev/null || echo UNKNOWN)
  case "$perm" in
    ADMIN|MAINTAIN|WRITE) role=maintainer ;;
    TRIAGE|READ) ;;
    *) perm_unread=1 ;; # gh failed, or answered nothing it names
  esac
  echo "Branch $(git rev-parse --abbrev-ref HEAD) vs $remote/main (before opening a PR)"
  issues=$(git log --format=%B "$tbase"..HEAD | grep -oiE '(closes|fixes) #[0-9]+' | grep -oE '[0-9]+' | sort -u || true)
  body=${TRIAGE_BODY:-}
  title=$(head -n 1 <<<"$body")
fi
echo "Role: $role"

# Plain bash 3.2 (the macOS default): no associative arrays.
# `if`, not `&&`: a PR with no changed file reads one empty line, and a false
# test as the loop's last command would fail the subshell under `set -e`.
classified=$(while IFS=$'\t' read -r a d path; do
  if [ -n "$path" ]; then printf '%s\t%s\t%s\t%s\n' "$(kind "$path")" "$a" "$d" "$path"; fi
done <<<"$files")
# A size column counts only if it is all digits: a row that is not one adds nothing.
sum() { awk -F'\t' -v k="$1" -v c="$2" '$1 == k && $c ~ /^[0-9]+$/ { s += $c } END { print s + 0 }' <<<"$classified"; }
paths_of() { awk -F'\t' -v k="$1" '$1 == k { print $4 }' <<<"$classified"; }

prod=$(sum production 2)
echo "Size: production +$prod/-$(sum production 3) (cap $SIZE_CAP) · tests +$(sum test 2) · docs +$(sum docs 2) · generated +$(sum generated 2)"
echo "Areas: $(cut -f4 <<<"$classified" | cut -d/ -f1-2 | sort | uniq -c | sort -rn | awk 'NF { printf "%s(%s) ", $2, $1 }')"
docs_touched=$(cut -f4 <<<"$classified" | grep -m1 '^docs/' || true)
decision=$(cut -f4 <<<"$classified" | grep -m1 '^docs/decisions/D' || true)

# The profiles that apply (scripts/profiles.tsv): every row whose title types and paths both
# match, the strictest value of each column. A row of types `-` applies only when the message
# declares it (`Profile: <name>`, which `make ready PROFILE=` writes).
[ -r "$PROFILES" ] || { echo "pr-triage: $PROFILES is missing: the tier is read from it" >&2; exit 2; }
ptype=
[[ "$title" =~ ^([a-z]+)(\([^\)]*\))?(!?): ]] && ptype=${BASH_REMATCH[1]}
pbreaking=
{ [[ "$title" =~ ^[a-z]+(\([^\)]*\))?!: ]] || grep -q '^BREAKING CHANGE' <<<"$body"; } && pbreaking=1
declared=$(tr -d '\r' <<<"$body" | sed -n 's/^Profile:[[:space:]]*//p' | tr -d ' ')
prod_paths=$(paths_of production)
profiles= floor= process_hit= tierlist= lineno=0
while IFS=$'\t' read -r pname ptypes ppaths ptier ptrailers pfreeze pstanding extra || [ -n "$pname" ]; do
  lineno=$((lineno + 1))
  pname=${pname%$'\r'}
  case "$pname" in "" | "#"*) continue ;; esac
  if [ -z "${pstanding:-}" ] || [ -n "${extra:-}" ] || ! [[ "$pname" =~ ^[a-z][a-z0-9-]*$ ]]; then
    echo "pr-triage: $PROFILES:$lineno: row '$pname' needs seven tab-separated fields" >&2; exit 2
  fi
  pstanding=${pstanding%$'\r'}
  case "$ptier" in full | -) ;; *) echo "pr-triage: $PROFILES:$lineno: row '$pname': the tier is full or -" >&2; exit 2 ;; esac
  case "$pfreeze" in held | -) ;; *) echo "pr-triage: $PROFILES:$lineno: row '$pname': freeze is held or -" >&2; exit 2 ;; esac
  case "$pstanding" in no | -) ;; *) echo "pr-triage: $PROFILES:$lineno: row '$pname': standing is no or -" >&2; exit 2 ;; esac
  # grep -E answers 1 for no match and 2 for a pattern it cannot read: the latter is a malformed
  # row (fail closed), never "no path matched".
  matched=
  if [ "$ppaths" != "-" ]; then
    if matched=$(grep -E -e "$ppaths" <<<"$prod_paths"); then :; else
      [ "$?" = 1 ] || { echo "pr-triage: $PROFILES:$lineno: row '$pname': the path pattern is not a regular expression" >&2; exit 2; }
      matched=
    fi
  fi
  hit=
  if [ "$ptypes" = "-" ]; then
    grep -qxF "$pname" <<<"$declared" && hit=1
  elif [ "$ptypes" = "*" ]; then hit=1
  else
    case ",$ptypes," in *",$ptype,"*) [ -z "$ptype" ] || hit=1 ;; esac
    case ",$ptypes," in *,breaking,*) [ -z "$pbreaking" ] || hit=1 ;; esac
  fi
  [ -z "$hit" ] || [ "$ppaths" = "-" ] || [ -n "$matched" ] || hit=
  [ -n "$hit" ] || continue
  profiles+="${profiles:+ + }$pname"
  [ "$ptier" != full ] || floor=full
  [ "$pname" != process ] || [ "$pstanding" != no ] || process_hit=1
  if [ "$ptier" = full ] && [ "$ppaths" != "-" ]; then tierlist+=" — $pname: $(tr '\n' ' ' <<<"$matched")"; fi
done <"$PROFILES"
echo "Profile: ${profiles:-other}"
tier=
if [ "$floor" = full ]; then tier=full
elif [ "$prod" -le 50 ]; then tier=light
else tier=standard
fi
echo "Tier: $tier$tierlist"
# The standing no of the process row: today's warning (no standing approval covers a supply-chain file).
[ -z "$process_hit" ] || echo "WARN: supply-chain files changed — the user reads those hunks and decides the merge; no standing approval covers it"

for n in $issues; do
  labels=$(gh issue view "$n" --json labels \
    --jq '[.labels[].name] | join(",")' 2>/dev/null || echo '?')
  echo "Closes #$n [$labels]"
done
[ -n "$issues" ] || echo "Closes: nothing"
echo "CI: $ci"

# Counted for --gate: every BLOCK but the red CI, which must not feed back into the check itself.
blocked=0
[ "$ci" = FAILURE ] && echo "BLOCK: CI is red — nothing to review until it is green"
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
if [ -z "$docs_touched" ] && [ "$prod" -gt 50 ] && ! grep -qi '^Docs: none - *[^[:space:]]' <<<"${body//$'\r'/}"; then
  echo "WARN: production code changed and no docs/ page touched — check docs/index.md §Documentation maintenance rules"
fi
# Advisory, never blocking, and fail open: a broken map must not fail the required plan-gate check.
DOCS_PR_BODY=$body "$(dirname "$0")/doc-owners.sh" <<<"$classified" || echo "WARN: doc-owners.sh failed — the docs-owner check did not run"
# The acceptance criteria of the message, traced to the tests of the tree: branch mode only (a PR has no tree here), advisory and fail open as above.
[ $# -ge 1 ] || TRIAGE_BODY=$body "$(dirname "$0")/ac-trace.sh" || echo "WARN: ac-trace.sh failed — the criteria check did not run"
[ -n "$decision" ] && echo "Decision file: $decision"
# The line a fix names its origin on: a contributor's `Regression-of:`, a maintainer's
# `Introduced-by:` that is not `unknown` (cartographer-review-pr §When a defect escapes).
escape=$(tr -d '\r' <<<"$body" | grep -iE '^(regression-of|introduced-by):[[:space:]]*[^[:space:]]' |
  grep -viE '^introduced-by:[[:space:]]*unknown[[:space:]]*$' || true)
escape=${escape%%$'\n'*}
[ -n "$escape" ] && echo "$escape — an escape: once it lands, .claude/skills/cartographer-review-pr/scripts/eval-case.sh <sha> adds it to evals/cases.md (cartographer-review-pr §When a defect escapes)"
# A reviewer definition (either reviewer agent file, or the review skill's SKILL.md, whole: the
# triage reads paths only) is scored when it changes (cartographer-review-pr §Changing the
# reviewer): a new line in evals/scores.md, or a body line `Evals: none - <reason>`. Refused in
# the branch mode only (what make ready runs), for a maintainer or when the permission could not
# be read (fail closed: a failed gh call must not turn the BLOCK into a WARN); on a PR a WARN,
# never counted for --gate: a train's body is the lander's and cannot carry the change's line,
# and a contributor cannot run the maintainer's evals.
reviewers=$(cut -f4 <<<"$classified" | grep -xF -e .claude/agents/cartographer-contract-reviewer.md \
  -e .claude/agents/cartographer-security-reviewer.md -e .claude/skills/cartographer-review-pr/SKILL.md || true)
scored=$(awk -F'\t' '$4 == ".claude/skills/cartographer-review-pr/evals/scores.md" && $2 ~ /^[0-9]+$/ && $2 > 0' <<<"$classified")
if [ -n "$reviewers" ] && [ -z "$scored" ] && ! grep -qi '^Evals: none - *[^[:space:]]' <<<"${body//$'\r'/}"; then
  evals="a reviewer definition changed with no new line in evals/scores.md (run the evals, or say \"Evals: none - <reason>\")"
  if [ $# -eq 0 ] && [ "$role" = maintainer ]; then
    echo "BLOCK: $evals"
  elif [ $# -eq 0 ] && [ -n "$perm_unread" ]; then
    echo "BLOCK: $evals — the role could not be read (gh repo view failed), so the check fails closed"
  else
    echo "WARN: $evals"
  fi
fi
if [ $# -ge 1 ]; then
  # A title with no scope is fine (requireScope: false); an unlisted one fails pr-title after the push.
  title=$(jq -r .title <<<"$meta")
  if [[ "$title" =~ ^[a-zA-Z]+\(([^\)]*)\)!?: ]]; then
    allowed=$(title_scopes)
    IFS=',' read -ra tscopes <<<"${BASH_REMATCH[1]}"
    for sc in "${tscopes[@]}"; do
      sc=${sc// /}
      grep -qxF -- "$sc" <<<"$allowed" ||
        echo "WARN: PR title scope \"$sc\" is not in the pr-title list ($(tr '\n' ' ' <<<"$allowed" | sed 's/ $//; s/ /, /g'); .github/workflows/pr-title.yml)"
    done
  fi
  for section in '## Self-review' '## Docs' '## Revert'; do
    grep -qF "$section" <<<"$body" || echo "WARN: PR body has no \"$section\" section (.github/pull_request_template.md)"
  done
fi
[ "$GATE" = 1 ] && [ "$blocked" = 1 ] && exit 1
exit 0
