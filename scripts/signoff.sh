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
# targets (make -k: all of them, even after one fails) -> retry once the packages of the
# Go tests a red run names, when nothing else failed (step 4c) -> check HEAD did not move -> push
# (explicit remote and refspec) -> post the status -> re-run a failed ci.yml run of
# that sha. Any failure before the push changes nothing on the remote; any failure
# before the POST posts nothing.
#
#   SIGNOFF_MAKE  the command that runs make targets, split on whitespace (default
#                 `make`). A command that takes make's arguments and runs them on
#                 another machine from the same tree (for instance a wrapper that
#                 syncs the clean tree to a build host and runs make there); it
#                 must exit with make's code (2 for failed targets, which alone are
#                 retried: step 4c), and exit 75 (EX_TEMPFAIL) when the machine is
#                 unavailable: the gate then runs in CI instead (step 4b). Any other
#                 exit (255 when ssh or rsync dropped) is the wrapper's, not a test's:
#                 the signoff ends with `the build host failed (exit N) before the gate
#                 finished` and the lander gates the train again, no flake (step 4d); so
#                 does a 75 from a targeted retry or the deferred web-perf, which run after
#                 the gate started: CI cannot take over mid-gate. It
#                 gets plain words: `-k` and the targets, `test-rerun RERUN_PKGS=<a,b>`.
#                 The first call, `signoff-platform`, goes through the same rule: only
#                 126 and 127 (the wrapper is missing or not executable) are a
#                 configuration error, "check SIGNOFF_MAKE".
#   SIGNOFF_HOSTED  non-empty: no make here or on a build host, the gate is ci.yml run on
#                 GitHub (step 4b): every job of HOSTED_JOBS, Linux, Windows and macOS, in
#                 parallel. The maintainer's default and the lander's (D2040):
#                 one build host is one slot, the hosted runners are many.
#
# Exits: 0 signed off; 1 refused or red (a `failed (exit N): nothing pushed` line);
# 69 (EX_UNAVAILABLE) the wrapper failed in the middle of the gate, no test failed. The code
# is the signal scripts/land.sh reads: the log carries the change's own test output, so a
# line in it proves nothing. It is not 75 (EX_TEMPFAIL), which only SIGNOFF_MAKE and
# SIGNOFF_WINDOWS use, to say "the machine is unavailable, run the gate in CI": here the
# machine broke mid-gate, and the answer is to gate the same tree again. No make, go, ssh
# (255) or signal (128+n) exit is 69, and every command of this script is checked or ends in die.
#   SIGNOFF_WINDOWS a command that runs its arguments natively on Windows from the
#                 same tree (empty: no Windows leg). It gets scripts/test-shard.sh, every
#                 shard at once (SIGNOFF_WINDOWS_SHARDS, default 4), over the packages a
#                 code change of the branch can break (scripts/affected-pkgs.sh --code:
#                 a change of documents or scripts has no Windows leg), in parallel with
#                 the targets, and `go test -count=1 -timeout 20m <packages>` for the
#                 retry; a failure (exit 1, the shards') fails the signoff, an exit 75 is
#                 reported and skipped (test-windows on main stays the net), any other
#                 exit is the wrapper's: `the Windows machine failed (exit N) before the
#                 gate finished` (step 4d).
#   SIGNOFF_PROBE_FILE  the lander's file for the probe of a confirmed failure, with a token
#                 on file descriptor 3 (`3<<<"$token"`): the file gets `<token> <words>` when the
#                 run ends, the words of the `signoff: probe:` line (step 4c), and an empty line
#                 when nothing was confirmed; no token, nothing written. Both are taken before
#                 any command runs: the variable is unset and descriptor 3 closed, so no target
#                 sees the path or the token (scripts/land.sh probe_of accepts nothing without it).
#
# Plain bash 3.2 (the macOS default): no associative arrays, no GNU-only flags.
set -euo pipefail

# The lander's out-of-band channel for the probe (SIGNOFF_PROBE_FILE and file descriptor 3,
# scripts/land.sh): the path of a file this run fills, once, with `<token> <words>` (the words of
# its `signoff: probe:` line), the token being the line read from descriptor 3. The lander accepts
# the file only with its token, which a test on its machine cannot know: it is in no environment,
# no argv and no log, and descriptor 3 is closed here, before any command of this script runs.
# The path is read into a plain variable and removed from the environment too, so no child (make,
# the tests under test) inherits it; the file is written by the exit trap of step 4, after every
# target is done, so what a test wrote there is gone. Without a token, nothing is written.
probe_file=${SIGNOFF_PROBE_FILE:-} probe_token= probe_words=
unset SIGNOFF_PROBE_FILE
if [ -n "$probe_file" ]; then read -r -u 3 probe_token 2>/dev/null || probe_token=; fi
exec 3<&-
write_probe() {
  [ -n "$probe_file" ] && [ -n "$probe_token" ] || return 0
  if [ -n "$probe_words" ]; then printf '%s %s\n' "$probe_token" "$probe_words"; else echo; fi 2>/dev/null >"$probe_file" || true
}

# The targets a signoff runs; `web-gate` is added when the diff needs it. Only
# test-install stays post-merge (it runs on push to main); a broken e2e scenario must
# never land (D1750, #1743).
TARGETS="gate smoke-http smoke e2e"
WEB_TARGET="web-gate"
# The jobs of ci.yml a gate in CI (step 4b) waits for: the required `gate` (test, which runs
# the targets above, and the web legs) and every leg `main-status` watches on main, so a
# change signed off in CI is the change main's CI would pass, Windows and macOS included
# (D2040). forge and upgrade-e2e run on a manual run only, never on main.
HOSTED_JOBS="gate test-windows web demo-edge docker helm test-macos test-linux-nosystemd"

die() { echo "signoff: $*" >&2; exit 1; }

here=$(cd "$(dirname "$0")" && pwd)
cd "$(git rev-parse --show-toplevel)"


# signoff.sh --probe <linux|windows> <packages> <race packages> (comma-separated, `-` for
# none): the lander's bisection step (scripts/land.sh, D1878). It runs, on this
# tree, only the command the targeted retry below ran when it confirmed a red run (its
# `signoff: probe:` line): no refusal, no push, no status. Exit 0 green, 75 the machine is
# unavailable, the platform's own failure code red (make's 2 on linux, `go test`'s 1 on
# windows): any other exit (ssh's 255, a signal, a missing wrapper) is the wrapper's, not the
# tests', and is reported as 75 too: the lander then bisects with full gates, whose infra
# handling is aware of a lost host, instead of rejecting a change no test condemned.
if [ "${1:-}" = --probe ]; then
  plat=${2:?--probe <linux|windows> <packages> <race packages>} p0=${3:--} p1=${4:--}
  [ "$p0" != - ] || p0=
  [ "$p1" != - ] || p1=
  read -r -a mk <<<"${SIGNOFF_MAKE:-make}"
  rc=0 own=2
  # shellcheck disable=SC2086  # the package lists are word lists on purpose
  if [ "$plat" = windows ]; then
    own=1
    read -r -a wmk <<<"${SIGNOFF_WINDOWS:?the probe of a Windows failure needs SIGNOFF_WINDOWS}"
    [ -z "$p0" ] || "${wmk[@]}" go test -count=1 -timeout 20m ${p0//,/ } || rc=$?
  else
    [ -z "$p0" ] || "${mk[@]}" test-rerun RERUN_PKGS="$p0" || rc=$?
    [ "$rc" != 0 ] || [ -z "$p1" ] || "${mk[@]}" test-rerun RERUN_PKGS="$p1" RERUN_RACE=1 || rc=$?
  fi
  case "$rc" in 0 | 75 | "$own") ;; *) echo "signoff: the probe's machine failed (exit $rc): not a verdict" >&2; rc=75 ;; esac
  exit "$rc"
fi

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
# post the status only when every job of HOSTED_JOBS is green. SIGNOFF_HOSTED=1 starts here.
hosted() { # $1 = why the gate runs in CI
  echo "signoff: $1: ci.yml runs the gate instead, every job of: $HOSTED_JOBS" >&2
  still_head
  push
  local t0 id= v= i existing before run
  t0=$(date +%s)
  # Run ids only grow, and no clock is involved (createdAt is GitHub's, this host's may skew):
  # the run to follow is the first one newer than the newest that already exists for this
  # branch and sha. A retried signoff left a finished run there, whose verdict is not this
  # attempt's (same approach as scripts/hosted-check.sh).
  list_runs() {
    gh run list -R "$slug" --workflow ci.yml --branch "$branch" --event workflow_dispatch --commit "$sha" \
      --json databaseId --jq '.[].databaseId'
  }
  # Captured first: a failed list must stop here, not read as "no run yet" (which would
  # reopen the stale-run case).
  existing=$(list_runs 2>/dev/null) || die "cannot list the ci.yml runs of $branch: nothing posted"
  before=$(printf '%s\n' "$existing" | sort -n | tail -n 1)
  before=${before:-0}
  gh workflow run ci.yml -R "$slug" --ref "$branch" >/dev/null || die "cannot dispatch ci.yml on $branch: nothing posted"
  for i in $(seq 1 "${SIGNOFF_DISPATCH_TRIES:-30}"); do
    id=$(list_runs 2>/dev/null | sort -n | awk -v b="$before" '$1 > b { print; exit }' || true)
    [ -z "$id" ] || break
    sleep "${SIGNOFF_POLL:-10}"
  done
  [ -n "$id" ] || die "the dispatched ci.yml run of $sha did not appear: nothing posted"
  echo "signoff: waiting for ci.yml run $id" >&2
  # The verdict (hosted_verdict) is read as soon as it is known: the first red job ends the
  # wait, the rest of the run is cancelled (its runners are free for the next gate), and the
  # failing Go tests of its failed jobs are printed for the lander's failing().
  for i in $(seq 1 "${SIGNOFF_GATE_TRIES:-240}"); do
    v=
    run=$(gh run view "$id" -R "$slug" --json status,jobs 2>/dev/null) && v=$(printf '%s' "$run" | hosted_verdict) || true
    [ -z "$v" ] || break
    sleep "${SIGNOFF_POLL:-15}"
  done
  case "$v" in
    green) ;;
    red\ *)
      gh run cancel "$id" -R "$slug" >/dev/null 2>&1 || true
      hosted_failures "$run"
      die "the jobs ${v#red } of ci.yml run $id are red: nothing posted" ;;
    *) gh run cancel "$id" -R "$slug" >/dev/null 2>&1 || true
       die "the jobs of ci.yml run $id are unfinished after the wait: nothing posted" ;;
  esac
  secs=$(( $(date +%s) - t0 ))
  post "make $targets - github-actions/ci.yml run $id ($HOSTED_JOBS) - $(printf '%dm%02ds' $((secs / 60)) $((secs % 60)))"
  echo "signoff: ${sha:0:12} on $branch signed off by ci.yml run $id"
  exit 0
}

# hosted_verdict: reads `gh run view --json status,jobs` on stdin and prints `green` when every
# job of HOSTED_JOBS completed with success, `red <jobs>` as soon as one completed otherwise
# (failure, cancelled, skipped, timed out: a skipped required leg tested nothing), and nothing
# while they run. A leg of one of them (`test-windows (3/4)`, `web (browser)`: the aggregator
# completes only after all its legs) that fails is red at once. A job of HOSTED_JOBS missing
# from a completed run is red too: a renamed job must fail the gate, never wait it out.
hosted_verdict() {
  jq -r --arg want "$HOSTED_JOBS" '
    ($want | split(" ") | map(select(. != ""))) as $w
    | [.jobs[] | select(.name as $n | $w | index($n))] as $j
    | [.jobs[] | select(.name as $n | $w | any(. as $p | $n | startswith($p + " (")))
        | select(.status == "completed" and (.conclusion == "failure" or .conclusion == "timed_out")) | .name] as $legs
    | ([$j[] | select(.status == "completed" and .conclusion != "success") | .name] + $legs) as $red
    | [$w[] | select(. as $n | [$j[].name] | index($n) | not)] as $missing
    | if ($red | length) > 0 then "red " + ($red | join(" "))
      elif ($missing | length) == 0 and ([$j[] | select(.status == "completed")] | length) >= ($w | length) then "green"
      elif .status == "completed" then "red " + ($missing | join(" ")) + " (missing)"
      else "" end'
}

# hosted_failures <run JSON>: the `--- FAIL:` lines of the run's failed jobs, without the job,
# step and time columns of `gh run view --log`, so the lander's failing() names the tests.
hosted_failures() {
  local jid tab=$'\t' # BSD sed reads no \t in a regular expression
  for jid in $(printf '%s' "$1" | jq -r '.jobs[] | select(.status == "completed" and .conclusion == "failure") | .databaseId' 2>/dev/null); do
    gh run view -R "$slug" --job "$jid" --log 2>/dev/null |
      sed -E "s/^[^$tab]*$tab[^$tab]*$tab[^ ]+ //" | grep -E '^--- FAIL: ' | head -n 20 >&2 || true
  done
}

# 4d. A wrapper failure (#1843). Make fails with exit 2, the Windows shards
# with exit 1, an unavailable machine says 75: any other exit is the wrapper's (ssh and rsync
# exit 255 when the connection drops), the log may stop before the targets did and no test
# failed. The exit code, 69 (see Exits above), is what scripts/land.sh gate_once() reads, never a
# line of the log, which holds the change's own output: it gates the train again, bounded, and
# files no flaky issue. A wrapper that dies like this is not red. The line is for people.
hostrc=
host_die() { # $1 = what failed, $2 = its exit code
  echo "signoff: $1 failed (exit $2) before the gate finished: nothing pushed, nothing posted" >&2
  exit 69
}

# 4c. The targeted retry (D1755). When every failure of a red log is a
# named Go test (scripts/test-failures.sh: not a build, vet, smoke, e2e or web failure,
# a timeout, a panic or a data race), the packages of those tests run once more, whole
# and uncached, where they failed: the race ones as the `race` target runs them. Green:
# the tests are flaky, named on a `signoff: flaky:` line the lander files as a bug, and
# the signoff goes on, as the rest of the run (make -k) was green. Red: a `signoff:
# confirmed on a targeted retry:` line, which the lander reads as no flake. A retry
# that fails with an exit that is not the tests' own (make's 2, the Windows retry's 1) is a
# wrapper failure (4d): return 2, the exit in $hostrc. That includes 75: a retry that cannot
# run (the machine was lost between the gate and the retry) says nothing about the tests, and
# a signoff ending in the red line without a "confirmed" line made the lander re-run and, with
# the host still down, bisect; it exits 69 and the lander gates the same tree again (amends
# D1755: "a retry that cannot run fails the signoff like a red one"). Only plain words reach
# the wrappers: the packages joined by commas (RERUN_PKGS), never a regular expression.
uniq_words() { printf '%s\n' "$@" | sort -u | paste -sd ' ' -; }
flaky=
retry() { # $1 = linux|windows, $2 = the red log; 0 when the retry is green
  local plan prc=0 names= p0= p1= race pkg name rrc=0 own=2
  [ "$1" != windows ] || own=1
  plan=$("$here/test-failures.sh" "$2") || prc=$?
  [ "$prc" = 0 ] && [ -n "$plan" ] || return 1
  while read -r race pkg name; do
    names="$names ${pkg##*/}.$name"
    if [ "$race" = 1 ]; then p1="$p1 $pkg"; else p0="$p0 $pkg"; fi
  done <<<"$plan"
  # shellcheck disable=SC2086  # the package lists are word lists on purpose
  p0=$(uniq_words $p0) p1=$(uniq_words $p1)
  echo "signoff: retrying once on $1, the packages of:$names" >&2
  # shellcheck disable=SC2086
  if [ "$1" = windows ]; then
    "${wmk[@]}" go test -count=1 -timeout 20m $p0 || rrc=$?
  else
    [ -z "$p0" ] || "${mk[@]}" test-rerun RERUN_PKGS="${p0// /,}" || rrc=$?
    [ "$rrc" != 0 ] || [ -z "$p1" ] || "${mk[@]}" test-rerun RERUN_PKGS="${p1// /,}" RERUN_RACE=1 || rrc=$?
  fi
  case "$rrc" in
    0) echo "signoff: flaky:$names failed and passed on a targeted retry" >&2; flaky="$flaky$names"; return 0 ;;
    75) hostrc=75; echo "signoff: the targeted retry could not run (machine unavailable)" >&2; return 2 ;;
    "$own") echo "signoff: confirmed on a targeted retry:$names" >&2
       # The lander bisects a red train with this command only (signoff.sh --probe).
       p0=${p0// /,} p1=${p1// /,}
       probe_words="$1 ${p0:--} ${p1:--}"
       echo "signoff: probe: $probe_words" >&2
       return 1 ;;
    *) hostrc=$rrc; echo "signoff: the targeted retry did not run to the end (exit $rrc)" >&2; return 2 ;;
  esac
}

# 4e. What a red Windows leg printed, package by package (#1952, #1870). The leg's log is
# every package's `go test` output whole, one after the other (scripts/test-shard.sh): a
# package ends on its `ok`, `?` or `FAIL<TAB><package>` line. The output of each package that
# failed goes to the gate log from its first `--- FAIL`, `panic:`, `fatal error:` or data race
# line (a build failure from its first line), cut to its first 45 and last 15 lines when longer
# than 60 (a panic's header and the top of its trace are at the start, the verdict at the end),
# and of the packages past the 8th only the verdict line. What follows the last verdict (the
# wrapper's own words, a leg cut short) is printed as it is, its last 40 lines. A `tail` of the
# whole log used to cut the `panic:` header and every package but the last.
win_failures() { # $1 = the leg's log
  tr -d '\r' <"$1" | awk -v max=60 -v head=45 -v pkgs=8 '
    function flush(verdict,   i, start, n, from, label) {
      if (nb == 0) return
      if (verdict) { label = $2; shown++ }
      else label = "the end of the log"
      if (shown > pkgs && verdict) { print buf[nb]; nb = 0; return }
      start = 1
      if (verdict) for (i = 1; i <= nb; i++)
        if (buf[i] ~ /^(--- FAIL|panic: |fatal error: |WARNING: DATA RACE)/) { start = i; break }
      if (!verdict && nb > 40) start = nb - 39
      n = nb - start + 1
      if (n <= max) { for (i = start; i <= nb; i++) print buf[i] }
      else {
        for (i = start; i < start + head; i++) print buf[i]
        print "signoff: (" (n - max) " lines of the output of " label " cut)"
        for (i = nb - (max - head) + 1; i <= nb; i++) print buf[i]
      }
      nb = 0
    }
    /^(ok|\?)[ \t]/ { nb = 0; next }
    /^FAIL[ \t]+[^ \t]/ { buf[++nb] = $0; flush(1); next }
    { buf[++nb] = $0 }
    END { flush(0) }'
}

# 4. Targets. First the platform probe, so a broken wrapper is found before the gate is spent.
# SIGNOFF_HOSTED=1: no build host at all, the gate is ci.yml's (step 4b).
[ -z "${SIGNOFF_HOSTED:-}" ] || hosted "SIGNOFF_HOSTED is set"
read -r -a mk <<<"${SIGNOFF_MAKE:-make}"
prc=0
probe=$("${mk[@]}" -s --no-print-directory signoff-platform) || prc=$?
[ "$prc" != 75 ] || hosted "the build host is unavailable (exit 75)"
# 126/127: the wrapper is not executable or not found, a configuration error. Any other exit
# is the host failing at its first call (an ssh drop is the likeliest moment): gated again.
case "$prc" in
  0) ;;
  126 | 127) die "the platform probe failed (exit $prc): check SIGNOFF_MAKE" ;;
  *) host_die "the build host" "$prc" ;;
esac
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
mlog=$(mktemp "${TMPDIR:-/tmp}/signoff-make.XXXXXX")
trap '[ -z "$win_pid" ] || { pkill -TERM -P "$win_pid" 2>/dev/null; kill "$win_pid" 2>/dev/null; } || true; [ -z "$win_log" ] || rm -f "$win_log" "$win_log.end"; rm -f "$mlog"; write_probe' EXIT
# -k: a red run still runs every other target, so its log is the whole evidence (4c).
# Only make's own failure (exit 2) is retried, and on Windows the shards' (exit 1): any
# other exit is the wrapper's, and the log may then stop before the targets did.
set +e
# shellcheck disable=SC2086  # the targets are a word list on purpose
"${mk[@]}" -k $targets $defer 2>&1 | tee "$mlog"
rc=${PIPESTATUS[0]}
set -e
secs=$(( $(date +%s) - start ))
[ "$rc" != 75 ] || hosted "the build host is unavailable (exit 75)"
case "$rc" in
  0) ;;
  2) lrc=0; retry linux "$mlog" || lrc=$?
     [ "$lrc" != 2 ] || host_die "the build host" "$hostrc"
     [ "$lrc" = 0 ] || die "make $targets failed (exit $rc): nothing pushed, nothing posted" ;;
  *) host_die "the build host" "$rc" ;;
esac
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
    1) echo "signoff: the Windows leg is red: the output of its failing package(s):" >&2
       winout=$(win_failures "$win_log")
       if [ -n "$winout" ]; then printf '%s\n' "$winout" >&2; else tail -n 40 "$win_log" >&2; fi
       lrc=0; retry windows "$win_log" || lrc=$?
       [ "$lrc" != 2 ] || host_die "the Windows machine" "$hostrc"
       [ "$lrc" = 0 ] || die "the Windows leg failed (exit $wrc): nothing pushed, nothing posted" ;;
    *) tail -n 40 "$win_log" >&2; host_die "the Windows machine" "$wrc" ;;
  esac
fi

# The deferred web budget test, now that nothing else is running.
if [ -n "$defer" ]; then
  echo "signoff: web-perf, alone" >&2
  wprc=0; "${mk[@]}" web-perf NPM_CI= || wprc=$?
  case "$wprc" in
    0) ;;
    2) die "make web-perf failed (exit $wprc): nothing pushed, nothing posted" ;;
    *) host_die "the build host" "$wprc" ;;
  esac
fi

# 5. Still the same commit (the tree may differ: a web build rewrites tracked bundle files).
still_head

# 6. Push.
push

# 7. Status.
set -- $flaky
post "make $targets - $platform - $duration - $windows${flaky:+ - $# flaky retried}"

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
