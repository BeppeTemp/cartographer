#!/usr/bin/env bash
# ready.sh — a maintainer hands a finished, reviewed change to the lander (D1706).
#
#   make ready MSG=<file> [AFTER=<slug>] [HOLD=1] [REVIEW="<tier>, <n> rounds"]
#
# In the change's worktree: squashes the branch onto its merge-base with main into ONE
# commit whose message is MSG (a conventional title, changelog body lines on one line
# each, `Closes #n`/`Refs #n`), checks it as pr-title.yml would (scripts/commit-lint.sh)
# and the size cap as plan-gate would (scripts/pr-triage.sh), pushes it as
# `refs/heads/ready/<slug>` (slug = the branch name after its prefix) and starts the
# lander (scripts/land.sh --daemon) unless one already runs on this machine. The branch
# itself is left as it is. Running it again replaces the queued change.
#
# A change built on another queued change (its branch holds that change's commits) is
# queued with AFTER=<slug>: it is squashed onto the commit of the branch whose tree is
# `ready/<slug>`'s, as a child of `ready/<slug>`, so it carries only its own diff. Queue
# that change first, and again after it changes, then this one. Once the predecessor has
# landed (`ready/<slug>` is gone), AFTER=<slug> still works: the commit is squashed from the
# `Lander-Tip` of the predecessor's landed commit (a child of that head, after checking the
# landed commit is exactly that head's change). A slug neither queued nor
# landed is refused, with the rebase to run.
#
# Trailers the lander reads: `Lander-After: <slug>` (land after that change),
# `Lander-Hold: yes` (queued, not landed until queued again without it) and
# `Review: <tier>, <n> rounds` (the review record, copied into the train PR).
# The lander keeps two trailers on the landed commit, `Lander-Slug: <slug>` and
# `Lander-Tip: <the branch head squashed>` (added here), which `make worktree-clean`
# reads; any of them in the message is dropped first.
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

"$here/upstream-repo.sh" --require-remote >/dev/null || die "cannot tell the upstream repository"
repo=$("$here/upstream-repo.sh")
perm=$(gh repo view "$repo" --json viewerPermission --jq .viewerPermission 2>/dev/null || echo UNKNOWN)
case "$perm" in
  ADMIN | MAINTAIN | WRITE) ;;
  *) die "permission on $repo is $perm: a contributor opens a PR instead" ;;
esac
git remote get-url upstream >/dev/null 2>&1 && die "the lander works on the upstream repository itself: origin must be it, not a fork"

git fetch -q origin main "+refs/heads/ready/*:refs/remotes/origin/ready/*" || die "cannot fetch origin"
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
grep -vE '^(Lander-[A-Za-z]+|Review): ' "$MSG" >"$full" || true
"$here/commit-lint.sh" "$full" || die "the message does not pass the title and body rules"
args=()
[ -z "${AFTER:-}" ] || args+=(--trailer "Lander-After: $AFTER")
[ -z "${HOLD:-}" ] || args+=(--trailer "Lander-Hold: yes")
[ -z "${REVIEW:-}" ] || args+=(--trailer "Review: $REVIEW")
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

sha=$(git commit-tree "$(git rev-parse 'HEAD^{tree}')" -p "$parent" -F "$full")
out=$(git push -q --force origin "$sha:refs/heads/ready/$slug" 2>&1) || die "the push of ready/$slug failed: $out"
echo "ready: $branch queued as ready/$slug (${sha:0:12}): $(head -n 1 "$full")"
[ -n "${READY_NO_LANDER:-}" ] || "$here/land.sh" --daemon
