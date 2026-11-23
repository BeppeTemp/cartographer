#!/usr/bin/env bash
# ready.sh — a maintainer hands a finished, reviewed change to the lander (D1706).
#
#   make ready MSG=<file> [AFTER=<slug>] [HOLD=1] [NOW=1] REVIEW="<tier>, <n> rounds" [USAGE=<record>] [PROFILE=<name>]
#
# In the change's worktree: squashes the branch onto its merge-base with main into ONE
# commit whose message is MSG (a conventional title, changelog body lines on one line
# each, `Closes #n`/`Refs #n`), checks it as pr-title.yml would (scripts/commit-lint.sh),
# the lines its profiles require (a `fix`: `Found-by:`/`Introduced-by:`, scripts/profiles.tsv) and
# its `Cost:` line (scripts/trailers-lint.sh)
# and the size cap as plan-gate would (scripts/pr-triage.sh), pushes it as
# `refs/heads/ready/<slug>` (slug = the branch name after its prefix) and starts the
# lander (scripts/land.sh --daemon) unless one already runs on this machine or another
# machine's lander is gating a train (that one takes the change). The lander runs the
# scripts of origin/main, not this worktree's. The branch
# itself is left as it is. Running it again replaces the queued change.
#
# A change built on another queued change (its branch holds that change's commits) is
# queued with AFTER=<slug>: it is squashed onto the commit of the branch whose tree is
# `ready/<slug>`'s, as a child of `ready/<slug>`, so it carries only its own diff. Queue
# that change first, and again after it changes, then this one. Once the predecessor has
# landed (`ready/<slug>` is gone), AFTER=<slug> still works: the commit is squashed from the
# `Lander-Tip` of the predecessor's landed commit (a child of that head, after checking the
# landed commit is exactly that head's change). A slug neither queued nor
# landed is refused, with the rebase to run. When `ready/<slug>` is queued, its pre-check
# still running is cancelled: that change is no longer its unit's tip, and this change's
# pre-check replays the whole queued chain on the current main (scripts/ready-check-base.sh).
#
# Trailers the lander reads: `Lander-After: <slug>` (land after that change),
# `Lander-Hold: yes` (queued, not landed until queued again without it),
# `Lander-Now: yes` (NOW=1: no collection window, the lander composes at once) and
# `Review: <tier>, <n> rounds` (the review record, copied into the train PR).
# The lander keeps three trailers on the landed commit: `Lander-Slug: <slug>` and
# `Lander-Tip: <the branch head squashed>` (added here), which `make worktree-clean`
# reads, and the `Review` added here (the last one: the lander drops every other
# `Review` line); any copy of them in the message is dropped first.
#
# USAGE=<record> is the record of the usage test (docs/testing.md §Usage test): `pass, <n> entries`
# or `waived: <reason>`, written as the `Usage-test:` trailer next to `Review:`. A change whose
# production paths are on the triage's usage list (`Usage-test: required`) is refused without it;
# a change off the list needs none, and one given anyway is written.
#
# PROFILE=<name> declares a row of scripts/profiles.tsv that no title type selects (today `live`):
# the message then carries `Profile: <name>`, which reaches main. Any other name is refused,
# with the declared ones.
#
# A contributor does not queue: they open a PR, and CI tests it.
# Plain bash 3.2 (the macOS default).
set -euo pipefail

die() { echo "ready: $*" >&2; exit 1; }
here=$(cd "$(dirname "$0")" && pwd)
case "${MSG:-}" in "" | /*) ;; *) MSG=$PWD/$MSG ;; esac
cd "$(git rev-parse --show-toplevel)"

branch=$(git symbolic-ref --short -q HEAD || true)
[ -n "$branch" ] || die "HEAD is detached: check out the change's branch"
case "$branch" in
  main | train/* | ready/* | failed/*) die "on $branch: ready is run on a change's branch (make worktree-add SLUG=<slug>)" ;;
esac
slug=${branch#*/}
[[ "$slug" =~ ^[a-z0-9][a-z0-9-]*$ ]] || die "the branch name must end in a slug of lowercase letters, digits and dashes: $branch"
[ -z "$(git status --porcelain)" ] || die "the tree is not clean: commit or drop the changes first"
[ -n "${MSG:-}" ] && [ -s "$MSG" ] || die "MSG=<file> with the change's commit message is required"
review_re='^(light|fast|standard|full), [0-9]+ rounds?$'
[[ "${REVIEW:-}" =~ $review_re ]] ||
  die "REVIEW=\"<tier>, <n> rounds\" is required (tier: light, fast, standard or full; e.g. REVIEW=\"full, 2 rounds\"): the lander checks the record against the tier its own triage computes"

"$here/upstream-repo.sh" --require-remote >/dev/null || die "cannot tell the upstream repository"
repo=$("$here/upstream-repo.sh")
perm=$(gh repo view "$repo" --json viewerPermission --jq .viewerPermission 2>/dev/null || echo UNKNOWN)
case "$perm" in
  ADMIN | MAINTAIN | WRITE) ;;
  *) die "permission on $repo is $perm: a contributor opens a PR instead" ;;
esac
git remote get-url upstream >/dev/null 2>&1 && die "the lander works on the upstream repository itself: origin must be it, not a fork"

git fetch -q --no-tags origin main "+refs/heads/ready/*:refs/remotes/origin/ready/*" || die "cannot fetch origin"
[ -z "${AFTER:-}" ] || [[ "$AFTER" =~ ^[a-z0-9][a-z0-9-]*$ ]] || die "AFTER must be a slug of lowercase letters, digits and dashes: $AFTER"
base=$(git merge-base origin/main HEAD) parent=$base
if [ -n "${AFTER:-}" ] && after=$(git rev-parse -q --verify "refs/remotes/origin/ready/$AFTER^{commit}"); then
  # The commit of this branch whose tree is the queued change's: this change starts there.
  want=$(git rev-parse "$after^{tree}") base=
  for c in $(git rev-list "origin/main..HEAD"); do
    [ "$(git rev-parse "$c^{tree}")" = "$want" ] && { base=$c; break; }
  done
  [ -n "$base" ] || die "no commit of this branch matches ready/$AFTER: rebase on that change's branch as queued, then make ready again"
  parent=$after
elif [ -n "${AFTER:-}" ]; then
  # Not queued: either it landed (main holds it squashed, with the lander's trailers) or it
  # never was. A landed predecessor's branch head, when this branch holds it, is where this
  # change starts, on main; the commits before it are already there.
  # A fixed-string search narrows the log (a slug is not a regex); the trailer decides, whole.
  landed=
  trailer() { git log -1 --format=%B "$1" | git interpret-trailers --parse | sed -n "s/^$2: //p" | tail -n 1; }
  for c in $(git log origin/main --format=%H --fixed-strings --grep="Lander-Slug: $AFTER"); do
    [ "$(trailer "$c" Lander-Slug)" = "$AFTER" ] && { landed=$c; break; }
  done
  [ -n "$landed" ] || die "ready/$AFTER is not queued and nothing on main landed as $AFTER: rebase this branch onto origin/main (git rebase --onto origin/main <the predecessor's last commit>), or queue $AFTER first"
  tip=$(trailer "$landed" Lander-Tip)
  if tip=$(git rev-parse -q --verify "$tip^{commit}") && git merge-base --is-ancestor "$tip" HEAD; then
    # The trailers on main are text a contributor's squash-merged PR body can carry, and a
    # change landed with a conflict resolution differs from its tip: the tip is where this
    # change starts only if the landed commit is exactly the tip's change.
    # Limit: in a chain A -> B -> C whose first two landed, AFTER=B is refused (the merge-base
    # predates A, so the diff holds A and B against B alone); it fails safe, rebase as said.
    mb=$(git merge-base "$tip" "$landed^") || die "$AFTER landed from a head unrelated to its parent on main: rebase this branch onto origin/main (git rebase --onto origin/main <the predecessor's last commit>)"
    want=$(git diff "$mb" "$tip" | git patch-id --stable | cut -d' ' -f1)
    got=$(git diff "$landed^" "$landed" | git patch-id --stable | cut -d' ' -f1)
    [ -n "$want" ] && [ "$want" = "$got" ] || die "$AFTER landed with a diff other than its head's (a conflict resolution, or trailers that are not the lander's): rebase this branch onto origin/main (git rebase --onto origin/main <the predecessor's last commit>)"
    # A child of the tip: the queued diff is exactly tip..HEAD, whatever landed since.
    base=$tip parent=$tip
  else
    echo "ready: warning: $AFTER landed but its head is not in this branch: this change is squashed onto main" >&2
  fi
fi
[ "$(git rev-list --count "$base"..HEAD)" -gt 0 ] || die "the branch has no commit over its base"

full=$(mktemp "${TMPDIR:-/tmp}/ready-msg.XXXXXX")
trap 'rm -f "$full"' EXIT
grep -vE '^(Lander-[A-Za-z]+|Review|Profile|Usage-test): ' "$MSG" >"$full" || true
"$here/commit-lint.sh" "$full" || die "the message does not pass the title and body rules"
if [ -n "${PROFILE:-}" ]; then
  declared=$(tr -d '\r' <"$here/profiles.tsv" | awk -F'\t' '!/^#/ && $1 != "" && $2 == "-" { print $1 }')
  grep -qxF -- "$PROFILE" <<<"$declared" || die "PROFILE=$PROFILE is not a declared profile (scripts/profiles.tsv): $(tr '\n' ' ' <<<"$declared")"
  # Not a Lander- line: it stays on the commit that lands.
  git interpret-trailers --in-place --trailer "Profile: $PROFILE" "$full"
fi
why=$("$here/trailers-lint.sh" "$full" 2>&1) || die "the message does not say where the defect was found (Found-by, Introduced-by), or its Cost line is malformed: $why"
args=()
[ -z "${AFTER:-}" ] || args+=(--trailer "Lander-After: $AFTER")
[ -z "${HOLD:-}" ] || args+=(--trailer "Lander-Hold: yes")
[ -z "${NOW:-}" ] || args+=(--trailer "Lander-Now: yes")
args+=(--trailer "Review: $REVIEW")
# The branch head this commit squashes: with the slug it lets `make worktree-clean` prove
# the work of a worktree landed (a reused slug or a copied trailer has another head).
args+=(--trailer "Lander-Tip: $(git rev-parse HEAD)")
[ ${#args[@]} -eq 0 ] || git interpret-trailers --in-place "${args[@]}" "$full"

# The size cap, as plan-gate applies it to a PR: a `Size exception: <why>` line in the
# message is the maintainer's accepted exception.
triage=$(TRIAGE_BODY=$(cat "$full") TRIAGE_BASE=$base "$here/pr-triage.sh" 2>&1) || die "the triage did not run: $triage"
if blocks=$(grep '^BLOCK' <<<"$triage" | grep -v 'CI is red'); then
  die "the change does not pass the triage: $blocks"
fi

# A maintainer must be readable to queue: the triage reads the role itself, and an answer it could not
# read (gh failed there) must never read as a contributor's change, which has no usage test to owe.
grep -q '^Role: maintainer$' <<<"$triage" || die "the triage did not read you as a maintainer (gh could not answer): queue again when it does"
# The usage test: a change on the triage's list is tried on a built binary before it is queued,
# and the commit carries the record (a pass, or a waiver with its reason).
if [ -n "${USAGE:-}" ]; then
  [[ "$USAGE" != *$'\n'* && "$USAGE" =~ ^(pass,\ [0-9]+\ entr(y|ies)|waived:\ [^[:space:]].*)$ ]] ||
    die "USAGE=\"pass, <n> entries\" or USAGE=\"waived: <reason>\" (one line): $USAGE"
  git interpret-trailers --in-place --trailer "Usage-test: $USAGE" "$full"
elif need=$(grep '^Usage-test: required' <<<"$triage"); then
  die "the change touches ${need#Usage-test: required — }, which is tried on a built binary before it is queued: build this worktree's binary and run the usage test (skill cartographer-review-pr, \"Before opening your own PR, or queuing a change\"), then make ready USAGE=\"pass, <n> entries\"; a change that cannot be tried says why: USAGE=\"waived: <reason>\""
fi

# A pending decision this change cites that nothing before it adds: the lander numbers a train's
# decisions once, so a citation of one added by a later change of the same chain is legal, but one
# that no queued change adds is rejected when the train is composed. Only a warning: the change
# that adds it may not be queued yet. HEAD's tree holds the chain it follows (each queued commit
# is the predecessor's plus its own).
for ph in $(git diff -U0 "$base" HEAD -- . ':(exclude)*_test.go' ':(exclude)*/testdata/*' | sed -n 's/^+//p' |
  grep -oE 'D-new-[a-z0-9][a-z0-9-]*' | sort -u || true); do
  git cat-file -e "HEAD:docs/decisions/$ph.md" 2>/dev/null && continue
  git ls-tree --name-only origin/main docs/decisions/ | grep -E "^docs/decisions/D[0-9]+-${ph#D-new-}\.md\$" >/dev/null && continue
  echo "ready: warning: it cites $ph, which nothing queued before it adds: queue the change that adds it AFTER=$slug, or the lander rejects this one" >&2
done

sha=$(git commit-tree "$(git rev-parse 'HEAD^{tree}')" -p "$parent" -F "$full")
out=$(git push -q --force origin "$sha:refs/heads/ready/$slug" 2>&1) || die "the push of ready/$slug failed: $out"
echo "ready: $branch queued as ready/$slug (${sha:0:12}): $(head -n 1 "$full")"
if [ -n "${after:-}" ]; then
  # ready/$AFTER is no longer its unit's tip: the lander never reads its pre-check, and this
  # change's pre-check replays it too. Its run still going is cancelled to free the runners
  # (the lander reads a cancelled run as no verdict); best effort.
  cancelled=yes
  if ids=$(gh run list -R "$repo" --workflow ready-check.yml --commit "$after" --json databaseId,status \
    --jq '.[] | select(.status != "completed") | .databaseId' 2>/dev/null); then
    for id in $ids; do gh run cancel -R "$repo" "$id" >/dev/null 2>&1 || cancelled=; done
  else cancelled=; fi
  [ -n "$cancelled" ] ||
    echo "ready: warning: the pre-check of ready/$AFTER could not be cancelled: it runs on, and the lander does not read it" >&2
fi
[ -n "${READY_NO_LANDER:-}" ] || "$here/land.sh" --daemon
