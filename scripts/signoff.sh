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
#                 must exit non-zero when make does, and exit 75 (EX_TEMPFAIL) when
#                 the machine is unavailable: the gate then runs in CI instead (step 4b).
#   SIGNOFF_WINDOWS a command that runs its arguments natively on Windows from the
#                 same tree (empty: no Windows leg). It gets scripts/test-shard.sh, every
#                 shard at once (SIGNOFF_WINDOWS_SHARDS, default 4), over the packages a
#                 code change of the branch can break (scripts/affected-pkgs.sh --code:
#                 a change of documents or scripts has no Windows leg), in parallel with
#                 the targets; a failure fails the signoff, an exit 75 is reported and
#                 skipped (test-windows on main stays the net).
#
# Plain bash 3.2 (the macOS default): no associative arrays, no GNU-only flags.
set -euo pipefail

# The targets a signoff runs; `web-gate` is added when the diff needs it. Only
# test-install stays post-merge (it runs on push to main); a broken e2e scenario must
# never land (D1750, #1743).
TARGETS="gate smoke-http smoke e2e"
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
# The wall-clock budget of the web tests must not share the machine with the Windows
# leg (#1744): web-gate leaves it out and it runs after that leg has finished (step 4).
defer=
[ "$web" != true ] || [ -z "${SIGNOFF_WINDOWS:-}" ] || defer="SIGNOFF_DEFER_PERF=1"
win_pkgs=
if [ -n "${SIGNOFF_WINDOWS:-}" ]; then
  # Two steps, each status checked: a pipe into tr would hide a failure as an empty list.
  win_paths=$(git diff --name-only "$remote"/main...HEAD) || die "cannot list the changed paths against $remote/main"
  win_pkgs=$(printf '%s\n' "$win_paths" | "$here/affected-pkgs.sh" --code) || die "cannot compute the packages of the Windows leg"
  win_pkgs=$(printf '%s' "$win_pkgs" | tr '\n' ' ')
fi
win_pkgs=${win_pkgs% }
sha=$(git rev-parse HEAD)

push() { # explicit remote and refspec: a worktree branch tracks upstream/main, and a
  # bare `git push` would follow push.default=upstream to main.
  # Quiet, its output only on failure: GitHub's "Create a pull request" hint is noise here.
  local out
  out=$(git push -q --force-with-lease -u origin "$branch:$branch" 2>&1) || die "the push failed: nothing posted: $out"
}
post() { # $1 = the status description
  if ! gh api -X POST "repos/$slug/statuses/$sha" -f state=success -f context=signoff \
    -f description="${1:0:140}" >/dev/null; then
    die "the branch is pushed but the status is not posted: run make signoff again (a 422 about the commit means $sha is not on $slug yet: open the PR, then run it again)"
  fi
}
still_head() {
  [ "$(git rev-parse HEAD)" = "$sha" ] && [ "$(git symbolic-ref --short -q HEAD || true)" = "$branch" ] ||
    die "HEAD moved during the run: the new commit was not signed off, run it again"
}
# 4b. The machine of SIGNOFF_MAKE is unavailable: push, dispatch ci.yml on the branch and
# post the status only when its `gate` job (test and the web legs, ci-gate.sh) is green.
hosted() {
  echo "signoff: the build host is unavailable (exit 75): ci.yml runs the gate instead" >&2
  still_head
  push
  local t0 id= c= i
  t0=$(date +%s)
  gh workflow run ci.yml -R "$slug" --ref "$branch" >/dev/null || die "cannot dispatch ci.yml on $branch: nothing posted"
  for i in $(seq 1 "${SIGNOFF_DISPATCH_TRIES:-30}"); do
    id=$(gh run list -R "$slug" --workflow ci.yml --branch "$branch" --event workflow_dispatch --commit "$sha" \
      --json databaseId --jq '.[0].databaseId // empty' 2>/dev/null || true)
    [ -z "$id" ] || break
    sleep "${SIGNOFF_POLL:-10}"
  done
  [ -n "$id" ] || die "the dispatched ci.yml run of $sha did not appear: nothing posted"
  echo "signoff: waiting for the gate job of ci.yml run $id" >&2
  for i in $(seq 1 "${SIGNOFF_GATE_TRIES:-180}"); do
    c=$(gh run view "$id" -R "$slug" --json jobs \
      --jq '[.jobs[] | select(.name == "gate")][0] | if .status == "completed" then .conclusion else "" end' 2>/dev/null || true)
    [ -z "$c" ] || break
    sleep "${SIGNOFF_POLL:-10}"
  done
  [ "$c" = success ] || die "the gate job of ci.yml run $id is ${c:-unfinished}: nothing posted"
  secs=$(( $(date +%s) - t0 ))
  post "make $targets - github-actions/ci.yml run $id - $(printf '%dm%02ds' $((secs / 60)) $((secs % 60)))"
  echo "signoff: ${sha:0:12} on $branch signed off by ci.yml run $id"
  exit 0
}

# 4. Targets. First the platform probe, so a broken wrapper is found before the gate is spent.
read -r -a mk <<<"${SIGNOFF_MAKE:-make}"
prc=0
probe=$("${mk[@]}" -s --no-print-directory signoff-platform) || prc=$?
[ "$prc" != 75 ] || hosted
[ "$prc" = 0 ] || die "the platform probe failed: check SIGNOFF_MAKE"
platform=$(printf '%s\n' "$probe" | awk 'NF { l = $0 } END { print l }')
[ -n "$platform" ] || die "the platform probe printed nothing"
echo "signoff: running make $targets on $platform" >&2
start=$(date +%s)
# The Windows leg runs beside the targets; its log and end time go to temp files.
win_pid= win_log=
if [ -n "$win_pkgs" ]; then
  read -r -a wmk <<<"$SIGNOFF_WINDOWS"
  win_log=$(mktemp "${TMPDIR:-/tmp}/signoff-windows.XXXXXX")
  echo "signoff: Windows leg on $(printf '%s\n' $win_pkgs | wc -l | tr -d ' ') package(s)" >&2
  # shellcheck disable=SC2086  # the packages are a word list on purpose
  # Sharded as the CI's test-windows legs are (D785): internal/mcpserver alone is most of it.
  ( r=0; "${wmk[@]}" bash scripts/test-shard.sh "${SIGNOFF_WINDOWS_SHARDS:-4}" all $win_pkgs >"$win_log" 2>&1 || r=$?
    echo "$r $(date +%s)" >"$win_log.end" ) &
  win_pid=$!
fi
# The leg's wrapper is a child of the subshell: stop it too, or it keeps the machine busy.
trap '[ -z "$win_pid" ] || { pkill -TERM -P "$win_pid" 2>/dev/null; kill "$win_pid" 2>/dev/null; } || true; [ -z "$win_log" ] || rm -f "$win_log" "$win_log.end"' EXIT
rc=0
# shellcheck disable=SC2086  # the targets are a word list on purpose
"${mk[@]}" $targets $defer || rc=$?
secs=$(( $(date +%s) - start ))
[ "$rc" != 75 ] || hosted
[ "$rc" = 0 ] || die "make $targets failed (exit $rc): nothing pushed, nothing posted"
duration=$(printf '%dm%02ds' $((secs / 60)) $((secs % 60)))
windows="windows not run"
if [ -n "$win_pid" ]; then
  wait "$win_pid" || true
  win_pid=
  read -r wrc wend <"$win_log.end" || { wrc=1; wend=$(date +%s); }
  wsecs=$(( wend - start ))
  case "$wrc" in
    0) windows="windows $(printf '%dm%02ds' $((wsecs / 60)) $((wsecs % 60)))" ;;
    75) windows="windows unavailable"; echo "signoff: warning: the Windows machine is unavailable, leg skipped" >&2 ;;
    *) tail -n 40 "$win_log" >&2; die "the Windows leg failed (exit $wrc): nothing pushed, nothing posted" ;;
  esac
fi

# The deferred web budget test, now that nothing else is running.
if [ -n "$defer" ]; then
  echo "signoff: web-perf, alone" >&2
  "${mk[@]}" web-perf NPM_CI= || die "make web-perf failed: nothing pushed, nothing posted"
fi

# 5. Still the same commit (the tree may differ: a web build rewrites tracked bundle files).
still_head

# 6. Push.
push

# 7. Status.
post "make $targets - $platform - $duration - $windows"

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
