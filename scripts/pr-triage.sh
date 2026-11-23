#!/usr/bin/env bash
# pr-triage.sh — the cheap first pass of a PR review (cartographer-review-pr skill §1).
#
#   pr-triage.sh <pr-number>   a pull request on GitHub (needs gh, jq)
#   pr-triage.sh --gate <pr>   the same report; exits 1 iff a BLOCK other than "CI is red"
#                           was printed (the plan-gate workflow runs this, D955)
#   pr-triage.sh --title-scopes  the scopes a PR title may use, as pr-title.yml lists them
#   pr-triage.sh --kinds     the NUL-separated paths on stdin, as <kind><TAB><path> lines (production, test, docs, generated)
#   pr-triage.sh               the current branch against upstream/main (origin/main when
#                           there is no `upstream` remote), before opening a PR; TRIAGE_BODY
#                           is the body it will carry (scripts/ready.sh passes the message),
#                           TRIAGE_BASE the commit it is measured from (default: the merge-base),
#                           TRIAGE_ROLE=maintainer the role, for the lander, which reads no permission
#
# Prints at most ~20 lines: size split by kind, areas, review tier, closed issues,
# CI, docs and decision hints (the docs-owner WARN comes from doc-owners.sh), in the branch
# mode the acceptance criteria line (ac-trace.sh), and the BLOCK/WARN lines that send a PR
# back without anyone reading its code. It reads metadata, never the diff body; the branch
# mode also reads the tree at HEAD, for the tests that cite a criterion.
set -euo pipefail
# Bytes, never characters: a path with a byte that is no valid UTF-8 must not be an "illegal byte sequence"
# to cut, grep or awk, which would read as "not found" below.
export LC_ALL=C

# Limits are CONTRIBUTING.md §Pull requests; change them there and here together.
SIZE_CAP=500      # production lines added per PR
FAST_CAP=150      # production lines added, at most, by a change of the fast lane
# What a kind of change owes (its review tier floor, its required trailers, whether it is held
# in the freeze, whether a standing approval may queue it) is data: scripts/profiles.tsv, read
# below, where the invariant areas and the supply-chain files are rows.
PROFILES=$(dirname "$0")/profiles.tsv
# The production paths whose change is tried before it is queued (`make ready USAGE=`,
# docs/testing.md §Usage test): what a user types, the clients it writes into, the installers and the bundled
# skills. By path and across the profiles (a `fix` and a `feat` both owe it), so not a profiles.tsv
# column. internal/mcpserver/ is left out: most changes touch it and few change what an agent reads.
USAGE_PATHS='^(cmd/cartographer/|internal/configurator/|internal/provisioning/|internal/skillbundle/bundled/|install\.sh$|install\.ps1$)'

kind() { # production | test | docs | generated
  case "$1" in
    internal/skillbundle/bundled/*) echo production ;; # shipped prompts, not docs
    internal/surveyor/*.md) echo production ;; # the prompts the Surveyor embeds (go:embed), not docs
    test/quarantine.tsv) echo production ;; # the list that switches tests off the verdict, not a test
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
# esc_path: a path as `@tsv` writes it (a backslash, a tab and a newline escaped), so that one file is
# one row of the list below whatever its name holds; every other byte, a quote included, stays as it is.
esc_path() {
  local p=$1 bs='\'
  p=${p//"$bs"/"$bs$bs"}; p=${p//$'\t'/"${bs}t"}; p=${p//$'\n'/"${bs}n"}
  printf '%s' "$p"
}
# --kinds: the paths on stdin, NUL-separated (git's -z: no quoting of a name), as `<kind><TAB><path>`
# lines (what scripts/status.sh counts production by).
if [ "${1:-}" = "--kinds" ]; then
  while IFS= read -r -d '' p; do [ -z "$p" ] || printf '%s\t%s\n' "$(kind "$p")" "$(esc_path "$p")"; done
  exit 0
fi

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
gitlinks=
if [ $# -ge 1 ]; then
  PR=$1
  meta=$(gh pr view "$PR" --json title,author,closingIssuesReferences,statusCheckRollup,body,headRefName,labels)
  # Not `gh pr view --json files`: it stops at 100 files, and every count below
  # (size, tier, supply chain, docs owners) would be taken on a truncated list.
  # `@tsv` escapes a tab, a newline or a backslash in a file name, so one file is one
  # row of three fields whatever its name holds; a string template would not. A renamed file adds a
  # second row for its previous name (size 0): a file moved out of a path a row covers is read there too.
  # The API lists neither a binary file's lines as `-` nor a gitlink's mode: a PR is not judged on them
  # here. The lander's branch-mode re-run (scripts/land.sh) enforces both on every queued change.
  files=$(gh_retry api --paginate "repos/{owner}/{repo}/pulls/$PR/files" --jq '.[] | ([.additions, .deletions, .filename] | @tsv), (select(.previous_filename != null) | [0, 0, .previous_filename] | @tsv)')
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
  # The lander (scripts/land.sh) passes TRIAGE_BASE and TRIAGE_ROLE=maintainer: it has fetched, and it
  # judges queued changes, which are maintainers': no fetch, no upstream lookup and no gh call here, so
  # a network failure cannot fail the judgement.
  lander=
  { [ -n "${TRIAGE_BASE:-}" ] && [ "${TRIAGE_ROLE:-}" = maintainer ]; } && lander=1
  if [ -z "$lander" ]; then
    # Fail closed on a fork clone without `upstream`: never diff against a stale fork main.
    "$(dirname "$0")/upstream-repo.sh" --require-remote >/dev/null || exit 2
    git fetch -q "$remote" main
  fi
  tbase=${TRIAGE_BASE:-$(git merge-base "$remote"/main HEAD)}
  # --no-renames: a rename is a delete and an add, so the old and the new path are both paths of the
  # change (a file moved into .github/workflows/ matches its row by the new one). A binary file
  # (numstat `-`) stays in the list: its path counts for the tier and the profiles, `sum` below adds no
  # size for it. -z: git lists a name as it is, never C-quoted, whatever it holds ("\303\250", a quote,
  # a backslash, a control character); esc_path then keeps one row per file.
  files=
  while IFS= read -r -d '' rec; do
    na=${rec%%$'\t'*} rest=${rec#*$'\t'}
    nd=${rest%%$'\t'*}
    files+="$na"$'\t'"$nd"$'\t'"$(esc_path "${rest#*$'\t'}")"$'\n'
  done < <(git diff --numstat -z --no-renames "$tbase" HEAD)
  # The sizes of a rename come from git's own pairing (-M): a pure move adds no line, a move with edits
  # adds the edited ones, both at the new path; the old path stays in the list with size 0.
  ren=
  while IFS= read -r -d '' rec; do
    na=${rec%%$'\t'*} rest=${rec#*$'\t'}
    nd=${rest%%$'\t'*}
    if [ -z "${rest#*$'\t'}" ]; then # a rename: the record is followed by the old and the new path
      IFS= read -r -d '' src || true
      IFS= read -r -d '' dst || true
      # Only a move inside one kind keeps git's sizes: a test, doc or generated file moved into
      # production is production lines in full (its old path's kind never counted them).
      [ "$(kind "$src")" != "$(kind "$dst")" ] || ren+="$na"$'\t'"$nd"$'\t'"$(esc_path "$src")"$'\t'"$(esc_path "$dst")"$'\n'
    fi
  done < <(git diff --numstat -z -M "$tbase" HEAD)
  if [ -n "$ren" ]; then
    files=$(awk -F'\t' -v OFS='\t' 'NR == FNR { ra[$4] = $1; rd[$4] = $2; old[$3] = 1; next }
      $3 in old { print 0, 0, $3; next } $3 in ra { print ra[$3], rd[$3], $3; next } { print }' <(printf '%s' "$ren") <(printf '%s' "$files"))
  fi
  # A gitlink (mode 160000, a submodule) is no file of the list's kinds: it keeps the change out of the fast lane.
  gitlinks=$(git diff --raw --no-renames "$tbase" HEAD | awk '$1 == ":160000" || $2 == "160000"')
  ci=local
  sizelabel=""
  if [ -n "$lander" ]; then perm=ADMIN; author=lander; else
    slug=$("$(dirname "$0")/upstream-repo.sh")
    author=$(gh api user --jq .login 2>/dev/null || echo me)
    perm=$(gh repo view "$slug" --json viewerPermission --jq .viewerPermission 2>/dev/null || echo UNKNOWN)
  fi
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
# A production path (the supply-chain files included) with a size that is not a number: a binary file.
nonnumeric=$(awk -F'\t' '$1 == "production" && ($2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+$/)' <<<"$classified")
# Its lines cannot be counted (a binary file, or any file a .gitattributes marks `-diff`: numstat
# prints `-`): it counts as over the size cap, so it is neither light nor fast and BLOCKs unless the
# message carries a `Size exception:`. A file read by size alone would pass any cap.
prod_shown=$prod
[ -z "$nonnumeric" ] || prod=$((prod + SIZE_CAP + 1))
echo "Size: production +$prod_shown/-$(sum production 3) (cap $SIZE_CAP${nonnumeric:+, counted over it: a file with no countable lines}) · tests +$(sum test 2) · docs +$(sum docs 2) · generated +$(sum generated 2)"
echo "Areas: $(cut -f4 <<<"$classified" | cut -d/ -f1-2 | sort | uniq -c | sort -rn | awk 'NF { printf "%s(%s) ", $2, $1 }')"
# The path list is computed once, and every test below reads it as a here-string: a producer piped into
# `grep -q` (or -m1) is read under pipefail as "not found" when grep leaves early (SIGPIPE) or cut fails.
allpaths=$(awk -F'\t' '{ print $4 }' <<<"$classified")
docs_touched=$(grep -m1 '^docs/' <<<"$allpaths" || true)
decision=$(grep -m1 '^docs/decisions/D' <<<"$allpaths" || true)

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
# The fast lane (CONTRIBUTING.md §Pull requests) sits between light and standard:
# a maintainer's change of 51 to FAST_CAP production lines that matches no path row with a tier floor (no
# invariant area, no supply-chain file: a path row is `full`), adds or edits no file under
# docs/decisions/ and holds no binary file and no gitlink. Paths, size, role and the kind of entry
# only: the triage stays blind to the diff body. Anything else keeps the tier it had.
fast=
if [ "$floor" != full ] && [ "$role" = maintainer ] && [ "$prod" -gt 50 ] && [ "$prod" -le "$FAST_CAP" ] &&
  ! grep -q '^docs/decisions/' <<<"$allpaths" && [ -z "$nonnumeric" ] && [ -z "$gitlinks" ]; then fast=1; fi
tier=
if [ "$floor" = full ]; then tier=full
elif [ "$prod" -le 50 ]; then tier=light
elif [ -n "$fast" ]; then tier=fast
else tier=standard
fi
echo "Tier: $tier$tierlist"
# A maintainer's change on the usage list is tried on a built binary before `make ready` (a
# contributor's PR is not concerned: it is the maintainer's queue that records the trial).
# An unreadable role (branch mode, gh failed) fails closed: the line is printed.
if [ "$role" = maintainer ] || [ -n "$perm_unread" ]; then
  usage_hit=$(grep -E -e "$USAGE_PATHS" <<<"$prod_paths" | tr '\n' ' ' || true)
  [ -z "$usage_hit" ] || echo "Usage-test: required — ${usage_hit% }"
fi
# The standing no of the process row: today's warning (no standing approval covers a supply-chain file).
[ -z "$process_hit" ] || echo "WARN: supply-chain files changed — the user reads those hunks and decides the merge; no standing approval covers it"

for n in $issues; do
  # The lander passes no gh: the labels are read for a person, not for its judgement.
  if [ -n "${lander:-}" ]; then labels=-; else
    labels=$(gh issue view "$n" --json labels \
      --jq '[.labels[].name] | join(",")' 2>/dev/null || echo '?')
  fi
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
reviewers=$(grep -xF -e .claude/agents/cartographer-contract-reviewer.md \
  -e .claude/agents/cartographer-security-reviewer.md -e .claude/skills/cartographer-review-pr/SKILL.md <<<"$allpaths" || true)
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
