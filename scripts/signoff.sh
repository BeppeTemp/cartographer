#!/usr/bin/env bash
# signoff.sh — a maintainer's `make gate` + `git push`, with an attestation.
#
#   make signoff            run the gate on this branch, push it, attest the pushed sha
#
# For a maintainer (ADMIN, MAINTAIN or WRITE on the upstream repository): CI does
# not repeat the gate on a maintainer's PR, its `gate` check passes iff a `signoff`
# commit status of a maintainer exists on the head sha, and this script posts it.
# A contributor just pushes the branch and opens the PR: CI tests it.
#
# In order: refuse (detached HEAD, main, a dirty tree, a contributor) -> run the
# targets -> check HEAD did not move -> push (explicit remote and refspec) -> post
# the status -> re-run a failed ci.yml run of that sha. Any failure before the push
# changes nothing on the remote; any failure before the POST posts nothing.
#
#   SIGNOFF_MAKE  the command that runs make targets, split on whitespace (default
#                 `make`). A command that takes make's arguments and runs them on
#                 another machine from the same tree (for instance a wrapper that
#                 syncs the clean tree to a build host and runs make there); it
#                 must exit non-zero when make does.
#
# Plain bash 3.2 (the macOS default): no associative arrays, no GNU-only flags.
set -euo pipefail

# The targets a signoff runs; `web-gate` is added when the diff needs it. The four
# post-merge targets (smoke-http, smoke, e2e, test-install) run on push to main.
TARGETS="gate"
WEB_TARGET="web-gate"

die() { echo "signoff: $*" >&2; exit 1; }

here=$(cd "$(dirname "$0")" && pwd)
cd "$(git rev-parse --show-toplevel)"

# 1. Refusals.
branch=$(git symbolic-ref --short -q HEAD || true)
[ -n "$branch" ] || die "HEAD is detached: check out the branch to sign off"
[ "$branch" != main ] || die "on main: signoff is for a feature branch (git switch -c feat/<slug>)"
dirty=$(git status --porcelain)
if [ -n "$dirty" ]; then
  echo "signoff: the tree is not clean; the tested tree must be HEAD:" >&2
  printf '%s\n' "$dirty" >&2
  exit 1
fi

# 2. Role: the viewer permission on the repository the PR targets.
"$here/upstream-repo.sh" --require-remote >/dev/null || die "cannot tell the upstream repository"
slug=$("$here/upstream-repo.sh")
perm=$(gh repo view "$slug" --json viewerPermission --jq .viewerPermission 2>/dev/null || echo UNKNOWN)
case "$perm" in
  ADMIN|MAINTAIN|WRITE) ;;
  *) die "permission on $slug is $perm: a contributor just pushes the branch and opens the PR, and CI tests it" ;;
esac

# 3. Base and web decision. The web path list has one source, ci-filters.sh.
remote=origin
git remote get-url upstream >/dev/null 2>&1 && remote=upstream
git fetch -q "$remote" main || die "cannot fetch $remote main (the merge-base must be current)"
web=$(git diff --name-only "$remote"/main...HEAD | "$here/ci-filters.sh" "$branch" | sed -n 's/^web=//p')
targets=$TARGETS
[ "$web" != true ] || targets="$targets $WEB_TARGET"

# 4. Targets. First the platform probe, so a broken wrapper is found before the gate is spent.
read -r -a mk <<<"${SIGNOFF_MAKE:-make}"
head_before=$(git rev-parse HEAD)
probe=$("${mk[@]}" -s --no-print-directory signoff-platform) || die "the platform probe failed: check SIGNOFF_MAKE"
platform=$(printf '%s\n' "$probe" | awk 'NF { l = $0 } END { print l }')
[ -n "$platform" ] || die "the platform probe printed nothing"
echo "signoff: running make $targets on $platform" >&2
start=$(date +%s)
rc=0
# shellcheck disable=SC2086  # the targets are a word list on purpose
"${mk[@]}" $targets || rc=$?
secs=$(( $(date +%s) - start ))
[ "$rc" = 0 ] || die "make $targets failed (exit $rc): nothing pushed, nothing posted"
duration=$(printf '%dm%02ds' $((secs / 60)) $((secs % 60)))

# 5. Still the same commit (the tree may differ: a web build rewrites tracked bundle files).
[ "$(git rev-parse HEAD)" = "$head_before" ] && [ "$(git symbolic-ref --short -q HEAD || true)" = "$branch" ] ||
  die "HEAD moved during the run: the new commit was not signed off, run it again"
sha=$head_before

# 6. Push: explicit remote and refspec. A worktree branch tracks upstream/main, and a
# bare `git push` would follow push.default=upstream to main.
git push --force-with-lease -u origin "$branch:$branch" || die "the push failed: nothing posted"

# 7. Status.
desc="make $targets - $platform - $duration"
if ! gh api -X POST "repos/$slug/statuses/$sha" -f state=success -f context=signoff \
  -f description="${desc:0:140}" >/dev/null; then
  die "the branch is pushed but the status is not posted: run make signoff again (a 422 about the commit means $sha is not on $slug yet: open the PR, then run it again)"
fi

# 8. Re-run a failed ci.yml run of this sha: its gate ran before the status existed.
# A run in progress is left alone, its gate is polling. Warnings only from here on.
if runs=$(gh run list -R "$slug" --workflow ci.yml --commit "$sha" --json databaseId,status,conclusion,event 2>/dev/null); then
  for id in $(printf '%s' "$runs" | jq -r '.[] | select(.status == "completed" and .conclusion == "failure") | .databaseId'); do
    gh run rerun -R "$slug" "$id" --failed >/dev/null 2>&1 || echo "signoff: warning: could not re-run run $id" >&2
  done
else
  echo "signoff: warning: could not list the ci.yml runs of $sha" >&2
fi

# 9. Summary.
echo "signoff: ${sha:0:12} on $branch signed off: make $targets, $platform, $duration"
if ! gh pr view "$branch" -R "$slug" --json number >/dev/null 2>&1; then
  echo "signoff: no open PR for $branch yet: gh pr create"
fi
