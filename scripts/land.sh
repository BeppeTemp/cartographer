#!/usr/bin/env bash
# land.sh — the lander: lands the maintainer's queued changes on main in trains (D1706).
#
#   land.sh            land trains until the queue holds nothing landable, then exit
#   land.sh --daemon   the same, detached (nohup, log in the state dir), unless one runs
#   land.sh --status   the queue, the open train PR, the rejected changes and a running lander's gate
#   land.sh --dry-run  compose the next train and print it: nothing gated, pushed or opened
#   --gate hosted|host with any of them but --status: where the train gate runs, hosted (the
#                      default: ci.yml on GitHub) or host (the build host, `make land GATE=host`)
#
# The queue is `refs/heads/ready/<slug>` on origin (scripts/ready.sh pushes them), oldest
# first, and it lands by unit. The `Lander-After:` trailer of a queued change, or of the
# commit a rejected one was queued as (`failed/<slug>^`), links it to that change when that
# one is queued or rejected (one that landed links nothing); a unit is a set of
# changes so linked, a change with no link a unit of one. A unit lands whole or not at all, its
# members in their Lander-After order (ties by queue order), the units by their oldest member;
# its tips are the members no other member follows. No member of a unit is picked while one is
# rejected (blocked), carries `Lander-Hold: yes` (held), or, while the freeze is open
# (scripts/freeze-open.sh, D1384), is a feat/perf/refactor or breaking change (frozen); a
# change queued again after its rejection is queued, its failed/ ref no longer counts. A train
# is main plus whole units, at most LAND_MAX (8) changes in all; a unit that does not fit waits
# for the next train, one larger than LAND_MAX is gated alone. Pending decisions are numbered once,
# after the last pick (D2304): the picks keep the `D-new-<slug>` placeholders; a citation must be
# numbered on main or added by a member of the change's own unit (earlier or later) or by an
# earlier unit, else that change is rejected and the queue composed again. Then each pending
# decision a change adds (`docs/decisions/D-new-<slug>.md`) gets the next number above both the
# highest on the tree and the newest issue or PR number (a contributor's decision takes its PR's
# number, D672: numbers above the counter keep the two apart and in time order), in commit order,
# and every `D-new-<slug>` citation is rewritten, each commit of the train being created again
# with its own tree (same message, author and dates). A citation of one whose
# change already landed (main holds `D<n>-<slug>.md`) reads as `D<n>`; an edit of its
# `D-new-<slug>.md` is applied to `D<n>-<slug>.md`; a change adding a `D-new-<slug>.md` whose
# slug main holds is rejected, naming the file.
#
# The train branch `train/<UTC time>` is gated by scripts/signoff.sh (the gate, the push and
# the `signoff` status: one gate for the whole train) in the lander's mode, and only its
# option chooses it (D2293). Hosted, the default (D2040), the gate is
# ci.yml on GitHub: every leg main's CI runs, Windows and macOS included, in parallel; a
# prefix of a bisection is gated the same way. With --gate host it runs on the build host
# (the web legs and the Windows leg when needed, the CI fallback on exit 75). The mode is
# logged at start and on each train line, and the train PR's third line is `Gate: <the
# signoff status of its head, up to " - make">`, or `Gate: unknown (...)`. On the build host the signoff itself retries once the
# Go tests a red run names (its step 4c): green, the train lands on that gate and a `bug`
# issue names them; red, the failure is confirmed and bisection starts without a re-run.
# A signoff that exits 69 (ssh or rsync dropped, so no test failed: #1843; its line `the build
# host failed (exit N) before the gate finished` is for people: the log holds the change's own
# output, so the code decides) is not red: the same tree is gated again, no flaky
# issue, up to LAND_INFRA_TRIES gates in a row; a host that stays down stops the lander with
# the queue untouched. Any other red: one re-run of the same tree; green then means a flaky
# test, so the train lands and a `bug` issue names the tests. Red twice: bisection over the
# train's prefixes that end at a unit's boundary finds the first failing unit, whose last tip
# in queue order is rejected (`the train gate is red with the chain <first>..<tip> and green
# without it`; a unit of one is the change), its other members staying queued, and the green
# prefix before it lands; when the signoff confirmed the failure on named Go tests, each prefix runs only those
# tests' packages (`signoff.sh --probe`, D1878) and the green prefix then gets
# one full gate before it lands. Green: one PR, title
# `chore(dev): train <id>, <k> changes`, rebase-merged so every change stays its own
# conventional commit on main, keeping its `Lander-Slug`, its `Lander-Tip` and the `Review`
# it was queued with; once merged, the landed queue entries are deleted (a queued
# change that main already holds, by its diff or by a commit carrying its `Lander-Slug` and
# `Lander-Tip` trailers, is dequeued, not rejected). A required check that fails is rerun once
# (a check reading the PR's diff fails with a 5xx right after it is opened): red again, the
# lander stops. The lander merges the PR
# itself once its required checks pass, and only while main is still the commit the train
# was built and gated on: `main`'s rule does not require a branch to be up to date, so a
# train merged after another merge would land a tree no gate proved. Main moved during the
# gate or before the merge: the PR is closed and the train composed and gated again. On macOS the
# lander holds `caffeinate -i` while it runs: an idle Mac on battery would sleep through it.
#
# Each queued change has a hosted pre-check (.github/workflows/ready-check.yml, run on the
# push of its ready/ ref, D1877): the Windows tests of the packages it can
# break and the frontend's unit tests, the legs `make check` does not run, on its queued chain
# replayed on the current main (scripts/ready-check-base.sh): the tip and the changes it
# follows through Lander-After. A unit's verdict is
# its tips' pre-checks (the others' are not read): red if one is red, else running if one runs,
# else green (green, or no verdict: none started within LAND_PRECHECK_START, still running past
# LAND_PRECHECK_WAIT, cancelled, or GitHub did not answer). A train of several units takes only
# green ones. When none is, the oldest unit whose verdict is running is gated alone, else the
# oldest red one: no gate waits on a pre-check, and a unit that breaks a leg costs one gate of
# its own instead of a red train and its bisection.
#
# A rejected change moves to `refs/heads/failed/<slug>`: a commit on top of it whose
# message is the reason, readable from any clone (`git log -1 origin/failed/<slug>`). A member
# rejected at its pick (a conflict, a decision) takes its unit out of the train: the train is
# reset to the commit before the unit's first pick, only
# that member moves to failed/ (its reason ends `(chain <first>..<tip>)`) and the others stay
# queued, blocked until it is queued again.
# One lander at a time: per machine a lock in the state dir, across machines an open PR
# whose head is `train/*`. A lander that finds one of the repository's own built on the current
# main adopts it (the same merge, the same rerun-once rule) and goes on with the queue; one
# built on an older main is closed and its changes composed again; a fork's, or one whose
# head it cannot match, is waited on for LAND_OPEN_TRIES polls, then the lander stops.
# A `train/*` branch on origin with no open PR that is not this machine's ($state/branch, the
# trains this machine composed and has not cleaned up) is another machine's lander gating it,
# while its tip is younger than LAND_TRAIN_STALE (21600 s): the lander waits on it as on that
# PR (until it goes stale, or LAND_OPEN_TRIES polls of its own when that is set, then it stops
# naming the branch), looks again right before its own gate (a composition is then dropped,
# nothing pushed), and --daemon starts nothing. LAND_FORCE=1 starts one anyway, and a lander
# run with it waits on no such train. Older, its lander died: the branch is deleted. This
# machine's own leftover (a lander that died after its push) is deleted at once. Nothing is
# deleted when the open PRs could not be listed in full. Limits: on the build host the branch
# reaches origin only after the gate, so this covers the hosted gate; two compositions pushed
# in the same second are still caught by the main checks.
#
# The lander runs origin/main's scripts wherever it is started (a change's worktree, older than
# main or removed while it gates): every mode but --status and --dry-run (which runs the
# scripts beside it) first resets its own checkout $state/src to main and runs that one's
# land.sh with the same options, unless a lander already runs or starts here (the start lock,
# below), whose checkout is left alone. Main is refs/remotes/origin/main, spelled out (a tag
# named origin/main would win over the short name), and no fetch of the lander or of
# scripts/ready.sh takes tags. Between two trains, when main's scripts/ changed, it resets that checkout and
# runs the new script (the same pid: the lock holds). LAND_SELF=1 runs the scripts beside this
# one instead (the tests, a lander change tried before it lands).
#
#   LAND_INFRA_TRIES (3)  LAND_INFRA_WAIT (120 s)
#   LAND_MAX (8)  LAND_POLL (30 s)  LAND_MERGE_TRIES (90 polls)  LAND_OPEN_TRIES (120 polls)  LAND_STATE (state dir)
#   LAND_TRAIN_STALE (21600 s: a train's tip keeps its composition time through the gate, the
#     re-run of a red one and a full bisection, several gates in all)  LAND_FORCE  LAND_SELF
#   LAND_START_TIMEOUT (300 s: the start lock of $state/src)
#   LAND_PRECHECK_START (300 s)  LAND_PRECHECK_WAIT (1200 s), from the queued commit's time
#   SIGNOFF_GATE is set by the lander (its --gate); the other SIGNOFF_* are passed to
#   signoff.sh unchanged.
#
# Plain bash 3.2 (the macOS default): no associative arrays. No pending decision is cited in
# this file: the tests queue copies of it, and a citation nothing in their queue adds is rejected.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
log() { echo "land: $(date -u +%H:%M:%S) $*" >&2; }
die() { log "$*"; exit 1; }

# The arguments, checked before anything starts: at most one of --daemon, --status and --dry-run,
# and --gate hosted|host with any but --status. The gate mode is that option and nothing else.
# SIGNOFF_GATE and SIGNOFF_HOSTED are never read to choose it: a variable exported in a shell
# profile, or inherited from the session that queued the first change (scripts/ready.sh starts
# the lander from there), put every lander on the build host without anyone choosing it that day
# (D2293). The lander sets SIGNOFF_GATE itself, below.
usage() { echo "usage: land.sh [--daemon | --dry-run] [--gate hosted|host] | land.sh --status" >&2; exit 2; }
action= gate_mode=hosted gate_given=false
while [ $# -gt 0 ]; do
  case "$1" in
    --daemon | --status | --dry-run) [ -z "$action" ] || usage; action=$1 ;;
    --gate)
      [ $# -ge 2 ] && ! $gate_given || usage
      case "$2" in hosted | host) gate_mode=$2 gate_given=true ;; *) usage ;; esac
      shift ;;
    *) usage ;;
  esac
  shift
done
[ "$action" != --status ] || ! $gate_given || usage

common=$(git -C "$here" rev-parse --path-format=absolute --git-common-dir)
main_repo=${common%/.git}
key=$(printf %s "$main_repo" | cksum | cut -d' ' -f1)
state=${LAND_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/cartographer-lander/$key}
mkdir -p "$state"
max=${LAND_MAX:-8}
poll=${LAND_POLL:-30}

# The checkout $state/src is reset by one starting lander at a time. The lock is a symlink,
# made atomically with its content, `<pid of its holder>.<epoch when taken>`; it covers the
# reset and the handoff only (the exec keeps the pid; --daemon holds it until the detached
# lander's pid file exists, then drops it; a foreground lander drops it once its pid file
# guards the checkout). It is taken over when its holder died, when it is older than
# LAND_START_TIMEOUT (300 s), or when it is not such a link and older than 5 s.
src_lock=$state/src.lock
lock_pid() { local t; t=$(readlink "$src_lock" 2>/dev/null) || return 0; echo "${t%%.*}"; }
take_src_lock() {
  local t p e age old now
  now=$(date +%s)
  # ln makes a link inside a directory found there (garbage, never one of these locks): not a take.
  if [ ! -d "$src_lock" ] || [ -L "$src_lock" ]; then ln -sn "$$.$now" "$src_lock" 2>/dev/null && return 0; fi
  if t=$(readlink "$src_lock" 2>/dev/null) && [[ "$t" =~ ^([0-9]+)\.([0-9]+)$ ]]; then
    p=${BASH_REMATCH[1]} e=${BASH_REMATCH[2]} age=$((now - e))
    if kill -0 "$p" 2>/dev/null && [ "$age" -lt "${LAND_START_TIMEOUT:-300}" ]; then return 1; fi
  else
    t=$(readlink "$src_lock" 2>/dev/null || true) e=$(date -r "$src_lock" +%s 2>/dev/null || echo 0)
    [ $((now - e)) -ge 5 ] || return 1
  fi
  # Renamed away, then checked: of two starters that found it stale, only the one that moved
  # what it read takes it over; the other gives back what it moved.
  old=$src_lock.stale.$$
  mv "$src_lock" "$old" 2>/dev/null || return 1
  if [ "$(readlink "$old" 2>/dev/null || true)" != "$t" ]; then
    ln -sn "$(readlink "$old")" "$src_lock" 2>/dev/null || true
    rm -f "$old"
    return 1
  fi
  rm -rf "$old"
  ln -sn "$$.$now" "$src_lock" 2>/dev/null
}
held_src_lock() { [ "$(lock_pid)" = "$1" ]; }
drop_src_lock() { ! held_src_lock $$ || rm -f "$src_lock"; }
# The repository, read through gh (a network call): main's land.sh, run in this lander's place
# while it holds the start lock (the exec, or --daemon's detached lander), takes it from the
# one that read it, so no network call runs under the lock. Read only by such a lander.
if [ -n "${LAND_EXEC_REPO:-}" ] && { held_src_lock $$ || held_src_lock "$PPID"; }; then
  repo=$LAND_EXEC_REPO
else
  repo=$(cd "$main_repo" && "$here/upstream-repo.sh")
fi
unset LAND_EXEC_REPO
g() { git -C "$main_repo" "$@"; }

running() { [ -f "$state/pid" ] && kill -0 "$(cat "$state/pid")" 2>/dev/null; }
# The gate mode of the running lander ($state/gate, beside its pid), or `not recorded` (one
# started before the file existed).
running_gate() { local m; m=$(cat "$state/gate" 2>/dev/null || true); echo "${m:-not recorded}"; }
# A foreground lander that finds one running: it names that one's gate and, asked for another
# mode, says the trains stay on it (the running lander keeps its mode until it exits).
already_running() {
  local m; m=$(running_gate)
  echo "land: a lander already runs here (pid $(cat "$state/pid"), gate: $m)"
  [ "$m" = "not recorded" ] || [ "$m" = "$gate_mode" ] ||
    echo "land: asked for gate $gate_mode: the trains stay on the running lander's gate ($m) until it exits"
}

fetch() { g fetch -q --no-tags --prune origin main '+refs/heads/ready/*:refs/remotes/origin/ready/*' '+refs/heads/failed/*:refs/remotes/origin/failed/*' '+refs/heads/train/*:refs/remotes/origin/train/*'; }
# "<number> <branch> <head sha> <true if from a fork> <base branch>" per open train PR. A list
# that may be cut (as many PRs as the limit) fails: a train whose PR it left out would read as
# having none, and be deleted.
open_trains() { gh pr list -R "$repo" --state open --limit 1000 --json number,headRefName,headRefOid,isCrossRepository,baseRefName --jq 'if length >= 1000 then error("the open PRs may be more than listed") else .[] | select(.headRefName | startswith("train/")) | "\(.number) \(.headRefName) \(.headRefOid) \(.isCrossRepository) \(.baseRefName)" end'; }

# foreign_trains <open train PR branches, one per line>: "<branch> <age of its tip in s>" per
# train/* branch on origin, as last fetched, that is neither this machine's ($state/branch)
# nor the head of an open PR.
foreign_trains() {
  local own b t now
  own=$(cat "$state/branch" 2>/dev/null || true)
  now=$(date +%s)
  while read -r b t; do
    if [ -n "$b" ] && ! grep -qxF "$b" <<<"$own" && ! grep -qxF "$b" <<<"$1"; then echo "$b $((now - t))"; fi
  done < <(g for-each-ref --format='%(refname:lstrip=3) %(committerdate:unix)' refs/remotes/origin/train/)
}
# young_train <open train PR branches>: the first foreign train younger than LAND_TRAIN_STALE,
# the one another machine's lander is gating; nothing when there is none.
young_train() {
  local b a
  while read -r b a; do
    if [ -n "$b" ] && [ "$a" -lt "${LAND_TRAIN_STALE:-21600}" ]; then echo "$b"; break; fi
  done < <(foreign_trains "$1")
}

# The lander runs origin/main's scripts, from its own checkout $state/src (see the header): a
# change's worktree holds the scripts of the main it branched from, and can be removed while
# its lander gates.
src=$state/src
# Main as fetched, spelled out: a tag named origin/main (on origin, or in the clone from any
# fetch that took tags) would win over the short name, and pick the scripts or the base.
main_ref=refs/remotes/origin/main
# refresh_src: creates $state/src at origin/main, or resets it there.
refresh_src() {
  if [ -e "$src/.git" ]; then
    git -C "$src" reset -q --hard "$main_ref" && git -C "$src" clean -qfd || die "cannot reset the lander's checkout of main, $src"
  else
    g worktree prune 2>/dev/null || true
    g worktree add -q --detach "$src" "$main_ref" >/dev/null 2>&1 || die "cannot create the lander's checkout of main, $src"
  fi
}
# run_main: replaces this process with $state/src's land.sh, given this one's options: the same
# --gate when one was given (the mode is that option only, D2293), never one from the environment.
# It runs from the main clone: started in a worktree that is then removed, every git call
# would fail on a missing working directory.
run_main() {
  cd "$main_repo"
  set --
  [ -z "$action" ] || set -- "$action"
  ! $gate_given || set -- "$@" --gate "$gate_mode"
  LAND_EXEC_REPO=$repo exec bash "$src/scripts/land.sh" "$@"
}
# A running lander's checkout is never reset under it (its next signoff would be another
# script's): then this one only says so (--daemon, a foreground lander). A --dry-run takes no
# lock and resets nothing: it runs the scripts beside it.
starting() { echo "land: a lander already runs here (starting, pid $(lock_pid))"; exit 0; }
if [ "$action" != --status ] && [ "$action" != --dry-run ] && [ -z "${LAND_SELF:-}" ] &&
  ! [ "$here/land.sh" -ef "$src/scripts/land.sh" ]; then
  g fetch -q --no-tags origin main || die "cannot fetch origin"
  take_src_lock || starting
  if running && [ "$(cat "$state/pid")" != $$ ]; then
    drop_src_lock
  else
    refresh_src
    run_main
  fi
fi

case "$action" in
  --daemon)
    if running; then drop_src_lock; echo "land: a lander already runs here (pid $(cat "$state/pid"), gate: $(running_gate))"; exit 0; fi
    drop_src_lock # no network call under the lock
    # A lander on another machine is gating a train: it takes this change too, none starts here.
    if [ -z "${LAND_FORCE:-}" ] && fetch 2>/dev/null && prs=$(open_trains 2>/dev/null); then
      f=$(young_train "$(cut -d' ' -f2 <<<"$prs")")
      if [ -n "$f" ]; then
        echo "land: a lander elsewhere is gating $f: not started here, the change is queued and that lander takes it (make land-status; if that lander is dead: LAND_FORCE=1 make land, or delete $f on origin)"
        exit 0
      fi
    fi
    # The handoff: the lock again (another lander may have started meanwhile), held until the
    # detached lander's pid file guards the checkout.
    if [ -z "${LAND_SELF:-}" ]; then
      take_src_lock || starting
      if running; then drop_src_lock; echo "land: a lander already runs here (pid $(cat "$state/pid"), gate: $(running_gate))"; exit 0; fi
    fi
    # The detached lander gets the option, never a mode through the environment: it inherits this
    # one unchanged and decides (and logs the notice below) itself.
    set --
    ! $gate_given || set -- --gate "$gate_mode"
    LAND_EXEC_REPO=$repo nohup "$0" "$@" >>"$state/log" 2>&1 </dev/null &
    child=$!
    if held_src_lock $$; then
      for _ in $(seq 1 100); do
        [ "$(cat "$state/pid" 2>/dev/null || true)" != "$child" ] && kill -0 "$child" 2>/dev/null || break
        sleep 0.1
      done
      drop_src_lock
    fi
    echo "land: lander started (pid $child, gate: $gate_mode), log $state/log"
    exit 0 ;;
esac

# The mode reaches signoff.sh through the environment, set once here: both signoff calls (the
# gate and the bisection probe) inherit the lander's mode, whatever the shell exported.
env_gate=${SIGNOFF_GATE:-}
unset SIGNOFF_HOSTED
export SIGNOFF_GATE=$gate_mode

# The queue, oldest first: "<slug> <sha>" lines.
queue() { g for-each-ref --sort=committerdate --format='%(refname:lstrip=4) %(objectname)' refs/remotes/origin/ready/; }
trailer() { g log -1 --format=%B "$2" | git interpret-trailers --parse | sed -n "s/^$1: *//p" | head -n 1; }
# The review record of a queued commit: its LAST `Review` trailer, the one `make ready REVIEW=`
# appended. ready.sh strips only `Review: ` with a space from the message, so an earlier
# `Review:light` survives there and interpret-trailers normalises it into a trailer.
review_of() { g log -1 --format=%B "$1" | git interpret-trailers --parse | sed -n "s/^Review: *//p" | tail -n 1; }
title() { g log -1 --format=%s "$1"; }

if [ "$action" = --status ]; then
  fetch || true
  echo "queue:"; queue | while read -r s sha; do
    extra=; a=$(trailer Lander-After "$sha"); [ -z "$a" ] || extra=" (after $a)"
    [ -z "$(trailer Lander-Hold "$sha")" ] || extra="$extra (held)"
    echo "  $s: $(title "$sha")$extra"
  done
  echo "open train PR:"; open_trains | cut -d' ' -f1,2 | sed 's/^/  #/' || true
  echo "rejected:"; g for-each-ref --format='%(refname:lstrip=4) %(contents:subject)' refs/remotes/origin/failed/ | sed 's/^/  /'
  [ ! -s "$state/blocked" ] || echo "stopped: $(cat "$state/blocked")"
  if running; then
    echo "lander: running (pid $(cat "$state/pid")), log $state/log"
    echo "gate: $(running_gate)"
  else
    echo "lander: not running"
  fi
  exit 0
fi
dry=false
[ "$action" != --dry-run ] || dry=true

# One lander per machine and repository.
if ! $dry; then
  if running && [ "$(cat "$state/pid")" != $$ ]; then already_running; exit 0; fi
  # The pid is already this one's when this lander ran main's new scripts between two trains.
  again=false
  [ "$(cat "$state/pid" 2>/dev/null || true)" != $$ ] || again=true
  echo $$ >"$state/pid"
  drop_src_lock # the pid file guards $state/src from here on
  echo "$gate_mode" >"$state/gate" # read by --status
  rm -f "$state/blocked"
  # The root this lander runs from, $state/src unless LAND_SELF=1: removing that worktree
  # deletes the scripts under it (scripts/lander-guard.sh, used by worktree-rm and
  # worktree-clean, reads this file).
  (cd "$here/.." && pwd -P) >"$state/root"
  trap 'rm -f "$state/pid" "$state/root" "$state/gate"' EXIT
  # The lander mostly waits (on the gate, on GitHub). A Mac on battery sleeps when idle, and
  # asleep the lander advances only in its dark wakes, a quarter of an hour apart, waking
  # with the network still down: caffeinate keeps it awake (not a closed lid) until the
  # lander exits.
  if ! $again && command -v caffeinate >/dev/null 2>&1; then caffeinate -i -w $$ >/dev/null 2>&1 </dev/null & fi
  # The permission is read again when GitHub does not answer (5xx, transport: an empty answer
  # or a failed call), as dequeue does: only a definite answer outside the maintainer roles
  # says the user is not one.
  perm=
  for try in 1 2 3 4 5; do
    perm=$(gh repo view "$repo" --json viewerPermission --jq .viewerPermission 2>/dev/null) || perm=
    [ -z "$perm" ] || break
    [ "$try" = 5 ] || sleep $((poll * try))
  done
  [ -n "$perm" ] || die "GitHub did not answer the permission read of $repo: try again"
  case "$perm" in ADMIN | MAINTAIN | WRITE) ;; *) die "permission on $repo is $perm: only a maintainer lands" ;; esac
fi
if [ "$gate_mode" = hosted ]; then
  hosted_jobs=$("$here/signoff.sh" --hosted-jobs 2>/dev/null) || hosted_jobs=
  read -r -a hosted_legs <<<"$hosted_jobs" || true
  log "gate: hosted, ci.yml, ${#hosted_legs[@]} legs"
else
  log "gate: host, the build host"
fi
if [ "$env_gate" = host ] && ! $gate_given; then
  log "the environment asks for the build host, the lander gates hosted: pass --gate host to use it"
fi

# reject <slug> <sha> <reason>: the change leaves the queue for failed/<slug>.
reject() {
  log "rejected $1: $3"
  $dry && return 0
  local f
  f=$(git -C "$main_repo" commit-tree "$2^{tree}" -p "$2" -m "lander: $3" -m "Fix it on the branch and run make ready again: once the slug is queued again this ref is ignored, and it is deleted when the change lands.")
  g push -q --force origin "$f:refs/heads/failed/$1" "--force-with-lease=refs/heads/ready/$1:$2" ":refs/heads/ready/$1" ||
    log "warning: could not move ready/$1 to failed/$1"
}

# dequeue <slug> <sha>: deletes ready/<slug> once its train landed, if it still holds <sha>.
# A push also fails for a reason that is not the lease (the network, just after a wake):
# that is retried, since a landed change left queued would come back as a conflict.
dequeue() {
  local try cur
  for try in 1 2 3 4 5; do
    g push -q origin "--force-with-lease=refs/heads/ready/$1:$2" ":refs/heads/ready/$1" >/dev/null 2>&1 && return 0
    if cur=$(g ls-remote origin "refs/heads/ready/$1" 2>/dev/null); then
      cur=${cur%%[[:space:]]*}
      [ -n "$cur" ] || return 0
      if [ "$cur" != "$2" ]; then log "warning: ready/$1 was queued again during the train: left in the queue"; return 0; fi
    fi
    [ "$try" = 5 ] || sleep $((poll * try))
  done
  return 1
}

# landed_on_main <slug> <sha>: 0 when a commit on main carries the Lander-Slug of the queued
# change and the Lander-Tip of its branch head: the change landed, whatever its patch looks like
# now (the lander numbers a pending decision differently on each landing, so a train merged by
# hand, or one whose queue entry outlived it, no longer matches by diff). A change queued
# without a Lander-Tip is not matched this way: a slug alone would also match its next version.
landed_on_main() {
  local tip c
  tip=$(trailer Lander-Tip "$2")
  [ -n "$tip" ] || return 1
  # A fixed-string search narrows the log (a slug is not a regex); the trailers decide, whole.
  for c in $(g log "$main_ref" --format=%H --fixed-strings --grep="Lander-Slug: $1"); do
    [ "$(trailer Lander-Slug "$c")" = "$1" ] && [ "$(trailer Lander-Tip "$c")" = "$tip" ] && return 0
  done
  return 1
}

breaking() { # a feat, perf or refactor title, or a breaking change: held while the freeze is open
  local t
  t=$(title "$1")
  [[ "$t" =~ ^(feat|perf|refactor)(\(|:|!) ]] || [[ "$t" =~ ^[a-z]+(\([^\)]*\))?!: ]] ||
    g log -1 --format=%B "$1" | grep -q '^BREAKING CHANGE'
}

# precheck <sha>: the verdict of the hosted pre-check of a queued change: ok, red or wait.
# An accelerator, never a gate: anything unreadable is ok, and the train gate proves it.
precheck() {
  local age v
  age=$(($(date +%s) - $(g log -1 --format=%ct "$1")))
  v=$(gh run list -R "$repo" --workflow ready-check.yml --commit "$1" --limit 1 --json status,conclusion \
    --jq 'if length == 0 then "none" else .[0].status + " " + (.[0].conclusion // "") end' 2>/dev/null) || { echo ok; return 0; }
  case "$v" in
    "completed success") echo ok ;;
    "completed failure" | "completed timed_out" | "completed startup_failure") echo red ;;
    completed*) echo ok ;; # cancelled or skipped: no verdict
    none) if [ "$age" -lt "${LAND_PRECHECK_START:-300}" ]; then echo wait; else echo ok; fi ;;
    *) if [ "$age" -lt "${LAND_PRECHECK_WAIT:-1200}" ]; then echo wait; else echo ok; fi ;;
  esac
}

# units <queue> <rejected slugs>: the units of the queue (see the header) into plain arrays (bash
# 3.2). Node i is a queued change, in queue order, or after them a rejected one (the commit it was
# queued as, failed/<slug>^): N_SLUG, N_SHA, N_REJ (1: rejected), N_PRED (the node its
# Lander-After names, -1 when that change landed or there is none) and N_COMP (its unit: the
# lowest node in it, so units sort by their oldest queued member).
units() {
  local s sha i j a n pass changed
  N_SLUG=() N_SHA=() N_REJ=() N_PRED=() N_COMP=()
  while read -r s sha; do
    [ -n "$s" ] || continue
    N_SLUG+=("$s") N_SHA+=("$sha") N_REJ+=(0)
  done <<<"$1"
  while read -r s; do
    [ -n "$s" ] || continue
    # Queued again after its rejection: the queued commit is the change, its failed/ ref is
    # stale (the lander deletes it once the change lands), so it blocks nothing.
    if grep -q "^$s " <<<"$1"; then continue; fi
    sha=$(g rev-parse -q --verify "refs/remotes/origin/failed/$s^" 2>/dev/null) || continue
    N_SLUG+=("$s") N_SHA+=("$sha") N_REJ+=(1)
  done <<<"$2"
  n=${#N_SLUG[@]}
  for ((i = 0; i < n; i++)); do
    N_PRED[i]=-1 N_COMP[i]=$i
    a=$(trailer Lander-After "${N_SHA[i]}")
    [ -n "$a" ] || continue
    for ((j = 0; j < n; j++)); do
      if [ "$j" != "$i" ] && [ "${N_SLUG[j]}" = "$a" ]; then N_PRED[i]=$j; break; fi
    done
  done
  # Each pass gives both ends of every edge the lower label: n passes connect any component.
  for ((pass = 0; pass < n; pass++)); do
    changed=false
    for ((i = 0; i < n; i++)); do
      j=${N_PRED[i]}
      if [ "$j" -lt 0 ] || [ "${N_COMP[j]}" = "${N_COMP[i]}" ]; then continue; fi
      if [ "${N_COMP[j]}" -lt "${N_COMP[i]}" ]; then N_COMP[i]=${N_COMP[j]}; else N_COMP[j]=${N_COMP[i]}; fi
      changed=true
    done
    $changed || break
  done
}

# unit_name <u>: the slug of a unit of one, `the chain <first>..<tip>` of a longer one.
unit_name() {
  if [ "${U_SIZE[$1]}" = 1 ]; then echo "${U_FIRST[$1]}"; else echo "the chain ${U_FIRST[$1]}..${U_TIP[$1]%% *}"; fi
}

# compose: selects the train into TRAIN (lines "<slug> <sha> <unit>"), in landing order: whole
# units, the members of each in their Lander-After order; U_SIZE, U_FIRST (the first member's
# slug) and U_TIP (the last tip in queue order, "<slug> <sha>") describe each unit by number.
# A blocked, held or frozen unit is not taken, nor one whose Lander-After lines loop; of the
# others, the units whose tips' pre-checks are green, as many as LAND_MAX changes hold, else
# one unit alone (see the header). The pre-checks of the members that are not tips are not read.
compose() {
  local q freeze=false failed n c i j p size order done_ found tip state v total ok_units= wait_unit= red_unit= u=0 lines
  q=$(queue)
  TRAIN=
  U_SIZE=() U_FIRST=() U_TIP=() U_LINES=()
  [ -n "$q" ] || return 0
  if GH_REPO=$repo "$here/freeze-open.sh" >/dev/null 2>&1; then freeze=true; fi
  failed=$(g for-each-ref --format='%(refname:lstrip=4)' refs/remotes/origin/failed/)
  units "$q" "$failed"
  n=${#N_SLUG[@]}
  for ((c = 0; c < n; c++)); do
    # A unit is visited at its label, its lowest node; one of rejected changes only has nothing to land.
    if [ "${N_COMP[c]}" != "$c" ] || [ "${N_REJ[c]}" = 1 ]; then continue; fi
    size=0 state=
    for ((i = 0; i < n; i++)); do
      [ "${N_COMP[i]}" = "$c" ] || continue
      if [ "${N_REJ[i]}" = 1 ]; then state=blocked; continue; fi
      size=$((size + 1))
      [ -n "$state" ] || [ -z "$(trailer Lander-Hold "${N_SHA[i]}")" ] || state=held
      if [ -z "$state" ] && $freeze && breaking "${N_SHA[i]}"; then state=frozen; fi
    done
    [ -z "$state" ] || continue
    # The landing order: the oldest queued member whose predecessor is already placed, each time.
    order= done_=" "
    for ((p = 0; p < size; p++)); do
      found=-1
      for ((i = 0; i < n; i++)); do
        if [ "${N_COMP[i]}" != "$c" ]; then continue; fi
        case "$done_" in *" $i "*) continue ;; esac
        j=${N_PRED[i]}
        if [ "$j" = -1 ]; then found=$i; break; fi
        case "$done_" in *" $j "*) found=$i; break ;; esac
      done
      [ "$found" -ge 0 ] || break
      done_="$done_$found "
      order=${order:+$order$'\n'}"${N_SLUG[found]} ${N_SHA[found]} $u"
    done
    if [ "$(grep -c . <<<"$order")" != "$size" ]; then
      log "${N_SLUG[c]}: its unit's Lander-After lines form a loop: not landable"
      continue
    fi
    # Its tips (no member follows them) decide its verdict; the last of them is its tip.
    v=ok tip=
    for ((i = 0; i < n; i++)); do
      [ "${N_COMP[i]}" = "$c" ] || continue
      for ((j = 0; j < n; j++)); do [ "${N_PRED[j]}" != "$i" ] || continue 2; done
      tip="${N_SLUG[i]} ${N_SHA[i]}"
      case "$v:$(precheck "${N_SHA[i]}")" in
        *:red) v=red ;;
        ok:wait) v=wait ;;
      esac
    done
    U_SIZE[u]=$size U_FIRST[u]=${order%% *} U_TIP[u]=$tip U_LINES[u]=$order
    case "$v" in
      ok) ok_units="$ok_units $u" ;;
      wait) [ -n "$wait_unit" ] || wait_unit=$u ;;
      red) [ -n "$red_unit" ] || red_unit=$u ;;
    esac
    u=$((u + 1))
  done
  total=0
  for u in $ok_units; do
    size=${U_SIZE[u]}
    if [ "$size" -gt "$max" ]; then
      [ "$total" = 0 ] || continue
      log "$(unit_name "$u") has $size changes, more than LAND_MAX ($max): gated alone"
      TRAIN=${U_LINES[u]}
      return 0
    fi
    [ $((total + size)) -le "$max" ] || continue
    TRAIN=${TRAIN:+$TRAIN$'\n'}${U_LINES[u]}
    total=$((total + size))
  done
  [ -z "$TRAIN" ] || return 0
  if [ -n "$wait_unit" ]; then
    TRAIN=${U_LINES[wait_unit]}
    if [ "${U_SIZE[wait_unit]}" = 1 ]; then
      log "${U_FIRST[wait_unit]}: its hosted pre-check is still running and nothing else is ready: gated alone"
    else
      log "$(unit_name "$wait_unit") (${U_SIZE[wait_unit]} changes): the hosted pre-check of its tip is still running and nothing else is ready: gated alone"
    fi
    return 0
  fi
  [ -n "$red_unit" ] || return 0
  TRAIN=${U_LINES[red_unit]}
  if [ "${U_SIZE[red_unit]}" = 1 ]; then
    log "${U_FIRST[red_unit]}: its hosted pre-check is red: gated alone"
  else
    log "$(unit_name "$red_unit") (${U_SIZE[red_unit]} changes): the hosted pre-check of its tip is red: gated alone"
  fi
}

# build_bundle <slug> <what>: builds the Atlas bundle again in the worktree (`make -s web`,
# LAND_BUNDLE_BUILD replaces it); its log is $state/bundle-<slug>.log. 1, with <what> said, when Node
# is missing or the build fails.
build_bundle() {
  local blog=$state/bundle-$1.log
  if [ -z "${LAND_BUNDLE_BUILD:-}" ] && ! command -v node >/dev/null 2>&1; then
    log "$1: $2 Node is not installed here: not rebuilt"
    return 1
  fi
  if ! ${LAND_BUNDLE_BUILD:-make -s web} >"$blog" 2>&1; then
    log "$1: $2 building it again failed (see $blog)"
    return 1
  fi
}

# bundle_pick <slug>: in a cherry-pick stopped on conflicts, those in internal/webui/dist only
# (the committed Atlas bundle, rebuilt whole by every frontend change: two of them always
# conflict there, never in web/src) are resolved by building the bundle again from the merged
# sources, and the pick goes on (D2040). The gate's TestBundleMatchesSource
# proves the result. 1 when another path conflicts, Node is missing, or the build fails: the
# change is then rejected as a conflict, as before. LAND_BUNDLE_BUILD replaces `make -s web`.
bundle_pick() {
  local u
  u=$(git diff --name-only --diff-filter=U 2>/dev/null) || return 1
  [ -n "$u" ] || return 1
  if grep -qv '^internal/webui/dist/' <<<"$u"; then return 1; fi
  build_bundle "$1" "only the Atlas bundle conflicts, but" || return 1
  git add -A internal/webui/dist && GIT_EDITOR=true git cherry-pick --continue >/dev/null 2>&1 || return 1
  log "$1: only the Atlas bundle conflicted: built again from the merged sources"
}

# landed_decision <slug>: prints D<n> when main ($base) holds docs/decisions/D<n>-<slug>.md,
# the decision the lander numbered when the change that added it landed; nothing otherwise.
landed_decision() {
  g ls-tree --name-only "$base" docs/decisions/ 2>/dev/null |
    sed -nE "s#^docs/decisions/(D[0-9]+)-$1\\.md\$#\\1#p" | head -n 1
}

# remap_pick <sha>: cherry-picks a change that edits docs/decisions/D-new-<slug>.md although
# main holds that decision as D<n>-<slug>.md (its first change landed and was numbered): the
# patch is applied to the numbered file, D-new-<slug> read as D<n> everywhere in it, as
# number_train rewrites a citation. 1 when no edit is of that kind or the patch does not apply.
remap_pick() {
  local f s num pairs= patch ph pair
  for f in $(git diff-tree --no-commit-id --name-only -r "$1" | grep -E '^docs/decisions/D-new-[a-z0-9-]+\.md$' || true); do
    [ ! -e "$f" ] && git cat-file -e "$1^:$f" 2>/dev/null || continue
    s=${f#docs/decisions/D-new-}; s=${s%.md}
    num=$(landed_decision "$s")
    [ -z "$num" ] || pairs="$pairs D-new-$s=$num"
  done
  [ -n "$pairs" ] || return 1
  patch=$(mktemp)
  git diff "$1^" "$1" >"$patch"
  for pair in $pairs; do
    ph=${pair%%=*}
    PH=$ph NUM=${pair#*=} perl -pi -e 'BEGIN { ($s = $ENV{PH}) =~ s/^D-new-//; } $t = m{^diff --git .*(_test\.go|/testdata/.*)$} if /^diff --git /; next if $t; s/\Q$ENV{PH}\E(?=\.md(?![a-z0-9-]))/$ENV{NUM}-$s/g; s/\Q$ENV{PH}\E(?![a-z0-9-])/$ENV{NUM}/g' -- "$patch"
  done
  if git apply --index "$patch" >/dev/null 2>&1; then
    rm -f "$patch"
    git commit -q --allow-empty -C "$1"
    return 0
  fi
  rm -f "$patch"
  git reset -q --hard
  return 1
}

# Pending decisions are numbered once, when the whole train is picked (D2304 amended): a pick
# never meets a renumbered line, so two changes editing a paragraph around a citation, or a later
# change editing an earlier one's D-new-<slug>.md, apply as written. Never rewrite fixtures: test
# files and anything under a testdata/ directory may spell a placeholder and are left alone. Not run
# in a subshell: PEND, MAP and CADDS carry state from the picks to the numbering.

# train_adds <sha>: the pending decisions (D-new-<slug> words) the commit adds.
train_adds() {
  git diff-tree --no-commit-id --name-only --diff-filter=A -r "$1" | sed -nE 's#^docs/decisions/(D-new-[a-z0-9-]+)\.md$#\1#p'
}

# train_touched <sha>: the files the commit touches that numbering may rewrite (not _test.go, not
# testdata/).
train_touched() {
  git diff-tree --no-commit-id --name-only -r "$1" | grep -vE '_test\.go$|(^|/)testdata/' || true
}

# pending_dup: after a cherry-pick, the only numbering check of the pick: sets DUP to the reason
# when the commit adds a pending decision whose slug main holds, or an earlier change of the train
# added; PEND lists the placeholders the picks so far added.
pending_dup() {
  local ph landed
  DUP=
  for ph in $(train_adds HEAD); do
    landed=$(landed_decision "${ph#D-new-}")
    if [ -n "$landed" ]; then
      DUP="it adds docs/decisions/$ph.md, but main already holds docs/decisions/$landed-${ph#D-new-}.md (two decisions with one slug)"
      return 0
    fi
    case " $PEND " in *" $ph "*)
      DUP="it adds docs/decisions/$ph.md, but an earlier change of the train added a decision with that slug"
      return 0 ;;
    esac
  done
  PEND="$PEND $(train_adds HEAD | tr '\n' ' ')"
}

# reject_at <i> <reason>: the member commits[i] of a composed train is rejected after the picks:
# only it moves to failed/ (reason ending with its chain), the rest of its unit stays queued and
# blocked. The caller composes again.
reject_at() {
  local u=${cunit[$1]} l
  l=$(sed -n "$(($1 + 1))p" <<<"$landed")
  if [ "${U_SIZE[u]}" = 1 ]; then reject "${l%% *}" "${l#* }" "$2"; else reject "${l%% *}" "${l#* }" "$2 ($(unit_name "$u" | sed 's/^the //'))"; fi
}

# citation_check: after the last pick, a pending decision a commit cites in the files it touches
# must be on main as D<n>-<slug>.md, or added by a member of the commit's own unit (earlier or
# later) or by an earlier unit. Sets CADDS (the placeholders each commit adds) and BAD ("<i> <where>"
# of the first commit of each unit that cites another, one per line).
citation_check() {
  local i j last allowed c f line ph n unit_done=
  CADDS=() BAD=
  for ((i = 0; i < k; i++)); do CADDS[i]=$(train_adds "${commits[i]}" | tr '\n' ' '); done
  for ((i = 0; i < k; i++)); do
    case " $unit_done " in *" ${cunit[i]} "*) continue ;; esac
    last=$i
    while [ $((last + 1)) -lt "$k" ] && [ "${cunit[last + 1]}" = "${cunit[i]}" ]; do last=$((last + 1)); done
    allowed=
    for ((j = 0; j <= last; j++)); do allowed="$allowed ${CADDS[j]}"; done
    c=${commits[i]}
    while IFS= read -r f; do
      [ -n "$f" ] && git cat-file -e "$c:$f" 2>/dev/null || continue
      while IFS= read -r line; do
        [ -n "$line" ] || continue
        n=${line%%:*} ph=${line#*:}
        case " $allowed " in *" $ph "*) continue ;; esac
        [ -z "$(landed_decision "${ph#D-new-}")" ] || continue
        BAD="${BAD:+$BAD$'\n'}$i $f:$n:$ph"
        unit_done="$unit_done ${cunit[i]}"
        break 2
      done < <(git show "$c:$f" | grep -noE 'D-new-[a-z0-9][a-z0-9-]*' || true)
    done < <(train_touched "$c")
  done
}

# number_train: the pending decisions of the whole train, in commit order then file order, each the
# next number above the highest on the tree and FLOOR; then every commit is created again in order
# (same message, author and dates; parent the previous re-created one) with its own tree, every
# pending file the train added so far renamed and the placeholders rewritten (the path form first,
# then the whole word: the one for slug `a` must not rewrite the one for `ab`) in the files the
# train touched so far. A citation of a decision main holds numbered reads as that number. The
# Atlas bundle is built again for a commit whose own diff adds a citation under web/ (WEBC, noted at
# the pick: remap_pick may have rewritten it already, and the bundle is stale all the same,
# TestBundleMatchesSource) and, after the first such commit, for each later one that touches web/
# or the bundle; the others take the previous commit's. commits holds the re-created commits. BUILD_FAIL is the
# index of the commit whose bundle could not be built (the worktree is then unusable: the caller
# composes again).
number_train() {
  local i j n ph c f pair num prev sofar rebuilt=false need slug newc cited
  BUILD_FAIL=
  MAP= newc=()
  n=$(ls docs/decisions | sed -nE 's/^D([0-9]+)-.*/\1/p' | sort -n | tail -n 1)
  n=${n:-0}
  [ "${FLOOR:-0}" -le "$n" ] || n=$FLOOR
  for ((i = 0; i < k; i++)); do
    for ph in ${CADDS[i]}; do n=$((n + 1)); MAP="$MAP $ph=D$n"; done
  done
  cited=$(for ((i = 0; i < k; i++)); do
    while IFS= read -r f; do
      [ -n "$f" ] && git cat-file -e "${commits[i]}:$f" 2>/dev/null || continue
      git show "${commits[i]}:$f" | grep -ohE 'D-new-[a-z0-9][a-z0-9-]*' || true
    done < <(train_touched "${commits[i]}")
  done | sort -u)
  for ph in $cited; do
    case " $MAP " in *" $ph="*) continue ;; esac
    num=$(landed_decision "${ph#D-new-}")
    [ -z "$num" ] || MAP="$MAP $ph=$num"
  done
  [ -n "$MAP" ] || [ -n "$WEBC" ] || return 0 # nothing to number, rewrite or build: the picked commits stand
  prev=$base sofar=
  for ((i = 0; i < k; i++)); do
    c=${commits[i]}
    git reset -q --hard "$c"
    sofar="$sofar"$'\n'"$(train_touched "$c")"
    for pair in $MAP; do
      ph=${pair%%=*} num=${pair#*=}
      while IFS= read -r f; do
        # A committed symlink is not followed (perl -pi would rewrite its target).
        [ -n "$f" ] && [ -f "$f" ] && [ ! -L "$f" ] || continue
        PH=$ph NUM=$num perl -pi -e 'BEGIN { ($s = $ENV{PH}) =~ s/^D-new-//; } s/\Q$ENV{PH}\E(?=\.md(?![a-z0-9-]))/$ENV{NUM}-$s/g; s/\Q$ENV{PH}\E(?![a-z0-9-])/$ENV{NUM}/g' -- "$f"
      done <<<"$sofar"
    done
    for ((j = 0; j <= i; j++)); do
      for ph in ${CADDS[j]}; do
        [ -f "docs/decisions/$ph.md" ] || continue
        num=$(tr ' ' '\n' <<<"$MAP" | sed -n "s/^$ph=//p")
        git mv "docs/decisions/$ph.md" "docs/decisions/$num-${ph#D-new-}.md"
      done
    done
    need=false
    case " $WEBC " in
      *" $i "*) rebuilt=true need=true ;;
      *) if $rebuilt && git diff-tree --no-commit-id --name-only -r "$c" | grep -E '^(web/|internal/webui/dist/)' >/dev/null; then need=true; fi ;;
    esac
    if $need; then
      slug=$(sed -n "$((i + 1))p" <<<"$landed"); slug=${slug%% *}
      build_bundle "$slug" "it cites a pending decision under web/, and" || { BUILD_FAIL=$i; return 0; }
    elif $rebuilt; then
      if [ -L internal/webui ] || [ -L internal/webui/dist ]; then
        log "internal/webui or its dist is a symlink: the bundle is not replaced"
        BUILD_FAIL=$i; return 0
      fi
      rm -rf internal/webui/dist && git checkout -q "$prev" -- internal/webui/dist
    fi
    git add -A
    git reset -q --soft "$prev"
    git commit -q --allow-empty -C "$c"
    prev=$(git rev-parse HEAD)
    newc+=("$prev")
  done
  commits=("${newc[@]}")
}

# The signoff's exit code for a wrapper that failed (ssh or rsync dropped: exit 255, or any exit
# that is not make's 2, the Windows shards' 1 or 75): no test failed, so it is neither red nor
# a flake (signoff.sh step 4d). EX_UNAVAILABLE, not 75 (EX_TEMPFAIL, the wrapper's "run the gate
# in CI"). An out-of-band signal: the gate log holds the change's own test output, so a line in
# it, which a test can print, must never decide that the gate was not red. Tests pin both scripts.
host_rc=69
infra_line='^signoff: the (build host|Windows machine) failed \(exit [0-9]+\) before the gate finished: nothing pushed, nothing posted$'
gate_token=
gate_once() { # $1 = log file; signoff.sh on the train branch: exit 0 green, 1 red, 2 not a test, 3 the build host failed
  local rc pf
  # The probe of a confirmed failure comes back out of band (SIGNOFF_PROBE_FILE, signoff.sh): the log
  # holds the change's own output, and so does anything a test can reach on this machine. The file is
  # made here, empty, and the signoff writes `<token> <words>` in it; the token is random per gate and
  # goes to the signoff on file descriptor 3, which it reads and closes before any target runs: it is
  # in no environment, no argv and no log, so only the signoff can write a line probe_of accepts.
  pf=$1.probefile
  rm -f "$pf"
  (umask 077; : >"$pf")
  gate_token=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n' || true)
  # Streamed to the lander's log as it runs (a gate takes minutes), and kept in $1.
  set +e
  (cd "$wt" && SIGNOFF_PROBE_FILE="$pf" "$here/signoff.sh" 3<<<"$gate_token") 2>&1 | tee "$1" >&2
  rc=${PIPESTATUS[0]}
  set -e
  # Only a red signoff (exit 1) can have confirmed a failure: any other exit leaves no probe.
  [ "$rc" = 1 ] || rm -f "$pf"
  [ "$rc" = 0 ] && return 0
  [ "$rc" != "$host_rc" ] || return 3
  # Known follow-up: these greps read the log, so a test that prints such a line can make a
  # signoff that failed for another reason read as red. That costs a re-run, and at worst a
  # bisection of a train that failed anyway; it cannot make a red gate read as infrastructure.
  if grep -qE 'failed \(exit [0-9]+\): nothing pushed, nothing posted|of ci.yml run [0-9]+ (is|are) ' "$1"; then return 1; fi
  return 2
}

# One gate; when the build host fails before it finishes (rc 3) the same tree is gated again,
# at most LAND_INFRA_TRIES (3) gates in a row with LAND_INFRA_WAIT (120 s) between them: no
# test failed, so no re-run of a red, no bisection and no flaky issue. A host that stays down
# stops the lander with the queue untouched, naming the failure. Each failed attempt's log is
# kept as $1.host<n>; $1 holds the last attempt, which is the one the caller reads.
gate() { # $1 = log file; exit 0 green, 1 red, 2 not a test (the caller dies)
  local rc n=1 max=${LAND_INFRA_TRIES:-3} why
  while :; do
    rc=0; gate_once "$1" || rc=$?
    [ "$rc" != 3 ] && return "$rc"
    # The words only (the exit code decided): the signoff prints its line last.
    why=$(grep -E "$infra_line" "$1" | tail -n 1 | sed 's/^signoff: //; s/: nothing pushed, nothing posted$//' || true)
    [ -n "$why" ] || why="the build host failed before the gate finished"
    [ "$n" -lt "$max" ] ||
      die "infrastructure, not a test: $why, in $n gates of $branch in a row (see $1): the queue is untouched"
    log "$why: gating $branch again ($n/$max done, in ${LAND_INFRA_WAIT:-120} s)"
    mv "$1" "$1.host$n"
    n=$((n + 1))
    sleep "${LAND_INFRA_WAIT:-120}"
  done
}

# safe_names: every name that reaches a title, a body or a commit message passes here (failing(),
# issue_title, flaky_issue): the gate log holds the change's own output, so a name read from it is
# attacker text. One name per line in, one inert name per line out. Only [A-Za-z0-9_.-] is kept (a `/`,
# as in the path of a vitest file, becomes `_`), plus the ` (panic)` / ` (panic: <message>)` suffix of failing() in its own charset [A-Za-z0-9 _:-]: no
# `@` (mention), `#` (issue reference), `[`, `(`, `!`, `/` or `:` (link, image, URL). What the charset
# still lets through is defused: a `www.` (GitHub links `www.example.org` unmarked) loses its dot,
# `www_`, a `GH-<digit>` (an issue autolink) becomes `GH_<digit>`, and a run of 7 to 40 lowercase hex
# digits (a commit link) gets a `_` after it (once: a name that went through is left as it is). $1 =
# the longest a name may be (none: no cut): the cut is part of the filter, because it can leave a hex
# run at the end of a name that had its `_`, or one `_` too many: the longest prefix that fits once
# defused is kept. A name that comes out empty is dropped. The same text serves the title, the body
# and the commit message of a rejection: the lander writes names into none of them any other way.
safe_names() { # $1 = longest name, optional
  LC_ALL=C awk -v cut="${1:-0}" '
    function defuse(s,   res, tok) {
      while (match(s, /[Ww][Ww][Ww]\./)) s = substr(s, 1, RSTART + 2) "_" substr(s, RSTART + 4)
      while (match(s, /[Gg][Hh]-[0-9]/)) s = substr(s, 1, RSTART + 1) "_" substr(s, RSTART + 3)
      res = ""
      while (match(s, /[A-Za-z0-9]+/)) {
        tok = substr(s, RSTART, RLENGTH)
        res = res substr(s, 1, RSTART - 1) tok
        s = substr(s, RSTART + RLENGTH)
        if (length(tok) >= 7 && length(tok) <= 40 && tok ~ /^[0-9a-f]+$/ && substr(s, 1, 1) != "_") res = res "_"
      }
      return res s
    }
    function inert(s,   suf, n, res) {
      suf = ""
      if (match(s, / \(panic(: [A-Za-z0-9 _:-]*)?\)$/)) { suf = substr(s, RSTART); s = substr(s, 1, RSTART - 1) }
      gsub(/\//, "_", s)
      gsub(/[^A-Za-z0-9_.-]/, "", s)
      if (s == "") return ""
      s = s suf
      res = defuse(s)
      if (cut > 0 && length(res) > cut)
        for (n = cut; n > 0; n--) { res = defuse(substr(s, 1, n)); if (length(res) <= cut) break }
      return res
    }
    { sub(/\r$/, ""); r = inert($0); if (r != "") print r }'
}

# The failures a red gate log names, in this order: the tests a signoff retry confirmed (the Windows
# leg's output has no `--- FAIL` line of the linux shape; they are what the change broke), the Go
# panics and fatal errors (`pkg.Test (panic)` when the test is known, else `pkg (panic: <message>)`,
# named by the `FAIL<TAB>pkg` line that ends the package), the e2e scenarios (`e2e.<scenario>`, from
# the `E2E FAILED: <scenario>: ...` lines of test/e2e/run.sh), then Go top-level tests, vitest cases,
# Playwright specs. At most 10, `pkg.Test` and `Test` once, each cut to 60 characters. The test of a
# panic is, in this order: the `--- FAIL` line directly before a `panic:` (a go test panic prints its
# test's verdict first; an earlier failed test is not the one that panicked, and a `fatal error:` has
# no such line), the `running tests:` entry go test prints after a timeout, the first `.TestX(` frame of
# the first goroutine's trace. A name is built from the charset [A-Za-z0-9_.-]; a panic's package from
# [A-Za-z0-9_-] and its message also spaces and `:`, no `.`: neither makes a link in the issue. Every
# name goes through safe_names, and is deduped in the form it is printed in (cut to 60), not before.
# $2 = lines: one name per line (the title of an issue is cut by names, see issue_title).
failing() { # $1 = gate log; vitest and Playwright colour their lines
  local clean confirmed panics pkeys e2e others
  clean=$(sed "s/$(printf '\033')\[[0-9;]*m//g" "$1" | tr -d '\r')
  confirmed=$(sed -n 's/^signoff: confirmed on a targeted retry: *//p' <<<"$clean" | tr ' ' '\n' | grep -E '^[A-Za-z0-9_.-]+$' | awk '!s[$0]++' || true)
  # Each line: the name, a TAB, the whole test (its name cut to 25 in the first field): the second
  # field says which plain `--- FAIL` test a panic's name already covers.
  panics=$(awk '
    function reset() { inpanic = 0; direct = ""; tn = ""; msg = ""; inrun = 0; ngo = 0 }
    function top(s) { sub(/^[ \t]*--- FAIL: /, "", s); sub(/[^A-Za-z0-9_].*$/, "", s); return s }
    /^[ \t]*--- FAIL: Test/ { if (!inpanic) direct = top($0); next }
    /^(panic: |fatal error: )/ {
      if (!inpanic) {
        inpanic = 1
        msg = $0; sub(/^(panic|fatal error): /, "", msg)
        if ($0 ~ /^panic: / && msg !~ /^test timed out/) tn = direct
      }
      direct = ""; next
    }
    inpanic && /^[ \t]+running tests:[ \t]*$/ { inrun = 1; next }
    inpanic && inrun && tn == "" && /^[ \t]+Test[A-Za-z0-9_]*(\/[^ \t]*)?[ \t]+\(/ { t = $1; sub(/\/.*$/, "", t); tn = t; next }
    /^goroutine [0-9]+ \[/ { if (inpanic) ngo++; next }
    inpanic && tn == "" && ngo <= 1 && /^[^ \t]/ && match($0, /\.Test[A-Za-z0-9_]+[.(]/) { tn = substr($0, RSTART + 1, RLENGTH - 2); next }
    /^(ok|\?)[ \t]/ { reset(); next }
    /^FAIL[ \t]+[^ \t]/ {
      if (inpanic) {
        pkg = $2; sub(/^.*\//, "", pkg); gsub(/[^A-Za-z0-9_-]/, "", pkg); pkg = substr(pkg, 1, 25)
        sub(/ \[recovered\]$/, "", msg); gsub(/[^A-Za-z0-9 _:-]/, "", msg); msg = substr(msg, 1, 50 - length(pkg)); sub(/ +$/, "", msg)
        if (pkg != "") print (tn != "" ? (pkg "." substr(tn, 1, 25) " (panic)\t" tn) : (pkg " (panic: " msg ")\t"))
      }
      reset(); next
    }
    { direct = ""; if (inrun) inrun = 0 }' <<<"$clean" | awk '!s[$0]++' || true)
  pkeys=$(cut -f2 <<<"$panics" | grep . || true)
  panics=$(cut -f1 <<<"$panics")
  e2e=$(sed -n 's/^E2E FAILED: \([A-Za-z0-9_.-][A-Za-z0-9_.-]*\): .*/e2e.\1/p' <<<"$clean" | awk '!s[$0]++' || true)
  others=$(sed -n \
    -e 's/^--- FAIL: \([A-Za-z0-9_]*\).*/\1/p' \
    -e 's/^ FAIL  \([^ ]*\) > .*/\1/p' \
    -e 's/^ *✘ *[0-9]* *\([^ ]*\) .*/\1/p' <<<"$clean" | LC_ALL=C sort -u | safe_names || true)
  # Through the environment: macOS awk refuses a newline in a -v value, and the lander runs there.
  # A first name covers the plain test it ends in, whole or cut: `pkg.Test (panic)` and `Test` are one
  # failure. The names are cut to 60 by safe_names, last, and deduped in the form they are printed in.
  printf '%s\n' "$others" | FIRST=$(printf '%s\n%s\n%s' "$confirmed" "$panics" "$e2e" | safe_names) KEYS=$pkeys awk '
    function emit(s) { if (!(s in out)) { out[s] = 1; print s } }
    function cover(t) { seen[t] = 1; seen[substr(t, 1, 60)] = 1 }
    BEGIN {
      n = split(ENVIRON["FIRST"], a, "\n")
      for (i = 1; i <= n; i++) if (a[i] != "") { emit(a[i]); t = a[i]; sub(/ .*$/, "", t); sub(/^.*\./, "", t); cover(t) }
      n = split(ENVIRON["KEYS"], a, "\n")
      for (i = 1; i <= n; i++) if (a[i] != "") cover(a[i])
    }
    $0 != "" && !($0 in seen) && !(substr($0, 1, 60) in seen) { emit($0) }' | safe_names 60 | awk '!s[$0]++' | head -n 10 | { if [ "${2:-}" = lines ]; then cat; else tr '\n' ' ' | sed 's/ $//'; fi; } || true
}
# The title of a flaky issue: "flaky: <names> <what>", at most 200 characters (GitHub refuses a title
# past 256, and issue_flaky would only warn: ten long names are 278). The names, one per line and each
# cut to 60 characters, are taken in order while they fit; the rest are `and N more` (the body keeps
# them all). The first name is always there.
issue_title() { # $1 = names, one per line; $2 = what happened
  NAMES=$(safe_names 60 <<<"$1") WHAT=$2 awk 'BEGIN {
    max = 200; head = "flaky: "; tail = " " ENVIRON["WHAT"]
    n = split(ENVIRON["NAMES"], raw, "\n"); m = 0
    for (i = 1; i <= n; i++) if (raw[i] != "") a[++m] = raw[i]
    for (k = m; k >= 1; k--) {
      s = a[1]; for (i = 2; i <= k; i++) s = s " " a[i]
      if (k < m) s = s " and " (m - k) " more"
      if (length(head s tail) <= max) break
    }
    print head s tail
  }'
}
# The first failing line of each e2e scenario, indented as a code block, for the body of an issue.
# The lines come from the gate log, which holds the output of the change under test: each is cut to
# printable ASCII (an embedded CR or an escape would break out of the block), loses its `@` (no
# mention) and is cut to 200 characters; at most 10.
e2e_report() { # $1 = gate log
  LC_ALL=C grep -a '^E2E FAILED: ' "$1" | head -n 10 | while IFS= read -r l; do
    l=$(printf '%s' "$l" | LC_ALL=C tr -cd '[:print:]' | tr -d '@' | cut -c1-200)
    printf '    %s\n' "$l"
  done || true
}
# The probe of a confirmed failure: `<linux|windows> <packages> <race packages>`, the words of the
# signoff's `signoff: probe:` line (signoff.sh step 4c), read from `$1.probefile`, the file the gate
# of log $1 handed to the signoff (gate_once), or nothing. Never from the log: it holds the change's
# own output, and a test can print a well-formed line when the real signoff printed none (a panic,
# a build failure) and so send the bisection after the wrong change. The file is not trusted
# either, a test on this machine can reach it: its last line counts only if it starts with $2, the
# token of that gate (gate_token), which only the signoff knew; a file without it, or no token, is
# no probe. The words are still checked before they become the arguments of `signoff.sh --probe`:
# the platform, then two comma-separated lists of [A-Za-z0-9_./-] words none of which starts with
# `-`, or `-` for none, not both. No file, an empty one or any other shape is no probe, and the
# bisection runs full gates. The token is compared by the shell, never given to another command.
# The file is that of the last gate (the re-run's when there was one: the first gate had no
# confirmed retry).
probe_of() { # $1 = the last gate's log, $2 = the token of that gate
  local line words
  [ -n "${2:-}" ] || return 0
  line=$(tr -d '\r' 2>/dev/null <"$1.probefile" | tail -n 1 || true)
  case "$line" in "$2 "*) words=${line#"$2 "} ;; *) return 0 ;; esac
  printf '%s\n' "$words" | awk '
    function ok(s,   n, w, i) {
      if (s == "-") return 1
      n = split(s, w, ",")
      for (i = 1; i <= n; i++) if (w[i] !~ "^[A-Za-z0-9_./][A-Za-z0-9_./-]*$") return 0
      return 1
    }
    NF == 3 && ($1 == "linux" || $1 == "windows") && ok($2) && ok($3) && ($2 != "-" || $3 != "-") { print $1, $2, $3 }' || true
}
# Both flaky bodies say how to take the test out of the gate while it is fixed (test/quarantine.tsv,
# D2294), then end with `Found-by: gate`, where the flake was found (the `ci-red`
# issue of a red main says `Found-by: main-ci`, scripts/main-red.sh).
issue_flaky() { # $1 = names, one per line, $2 = what happened, $3 = body
  local q="To take it out of the gate while it is fixed, add it to test/quarantine.tsv with this issue's number."
  gh issue create -R "$repo" --label bug --title "$(issue_title "$1" "$2")" --body "$3"$'\n\n'"$q"$'\n\n'"Found-by: gate" >/dev/null ||
    log "warning: could not open the flaky issue"
}
# A green gate whose signoff retried named tests: they are flaky, the train lands, a bug names them.
# The line is in the log among the tests' own output: only package.Test names make the title.
flaky_issue() { # $1 = gate log, $2 = what happened
  local names
  names=$(sed -n 's/^signoff: flaky: \(.*\) failed and passed on a targeted retry$/\1/p' "$1" |
    grep -E '^[A-Za-z0-9_.-]+( [A-Za-z0-9_.-]+)*$' | tr ' ' '\n' | safe_names | tr '\n' ' ' | sed 's/ $//' || true)
  [ -n "$names" ] || return 0
  log "green after the signoff's targeted retry: flaky $names"
  issue_flaky "$(tr ' ' '\n' <<<"$names")" "$2" \
    "The gate of the train \`$branch\` failed on $names and passed when the signoff ran their packages once more, the rest of the gate green. A retry is not a fix: find why they fail."
}

# gate_line <gated sha>: the `Gate:` line of the train PR, the gate the train actually had: the
# description of the newest successful `signoff` status of that sha (GitHub lists the newest first)
# up to its ` - make` (signoff.sh steps 4b and 7: `gate=hosted run=<id> legs=<n> <duration>`,
# `gate=host <platform> windows=<n> web=<yes|no> <duration>`). The status, never the gate log, which
# holds the change's own output. A status that cannot be read, is absent, or is not of that shape
# (the `gate=` token, then words of [A-Za-z0-9=/._-]: it goes into a public PR body) gives
# `Gate: unknown (...)`: never a reason to stop, the gate was green.
gate_line() {
  local d re='^gate=(hosted|host)( [A-Za-z0-9=/._-]+)+$'
  d=$(gh api "repos/$repo/commits/$1/statuses?per_page=100" \
    --jq '[.[] | select(.context == "signoff" and .state == "success")][0].description // ""' 2>/dev/null) || d=
  d=${d%% - make*}
  if [[ "$d" =~ $re ]]; then echo "Gate: $d"; else echo "Gate: unknown (the signoff status of ${1:0:12} could not be read)"; fi
}

# merge_train <PR> <gated head>: merges the train PR once its required checks pass, only while
# main is still $base (or is the train itself: an earlier merge call went through). 0: merged;
# 1: main moved, the PR is closed and the train must be composed again. Not auto-merge:
# GitHub would merge it onto whatever main is by then. The first red required check is
# rerun once (#1881: a check that reads the PR's diff fails with a 5xx right after the PR is
# opened, and passes a minute later); red again after that, the lander stops.
merge_train() {
  local v st ms cur id n reran=false settle=0
  for _ in $(seq 1 "${LAND_MERGE_TRIES:-90}"); do
    v=$(gh pr view "$1" -R "$repo" --json state,mergeStateStatus --jq '.state + " " + .mergeStateStatus' 2>/dev/null || true)
    st=${v%% *} ms=${v#* }
    [ "$st" != MERGED ] || return 0
    [ "$st" != CLOSED ] || die "train PR #$1 was closed by hand: the queue is untouched"
    if g fetch -q --no-tags origin main 2>/dev/null; then
      cur=$(g rev-parse "$main_ref")
      if [ "$cur" != "$base" ]; then
        [ "$(g rev-parse "$cur^{tree}")" != "$(g rev-parse "$2^{tree}")" ] || return 0
        log "main moved before train PR #$1 merged (${base:0:12}..${cur:0:12}): closing it, composing the train again"
        gh pr close "$1" -R "$repo" --delete-branch >/dev/null 2>&1 || log "warning: could not close train PR #$1"
        return 1
      fi
      case "$ms" in
        CLEAN | UNSTABLE | HAS_HOOKS)
          gh pr merge "$1" -R "$repo" --rebase --match-head-commit "$2" >/dev/null 2>&1 && return 0
          log "warning: GitHub refused the merge of train PR #$1 for now: asking again" ;;
        BLOCKED)
          # A workflow that never started will not turn green by waiting (#1872).
          if gh pr checks "$1" -R "$repo" --required --json state --jq '.[].state' 2>/dev/null | grep -qx STARTUP_FAILURE; then
            echo "a required check of train PR #$1 ended in startup_failure" >"$state/blocked"
            die "a required check of train PR #$1 ended in startup_failure: it will not turn green on its own, rerun or fix its workflow; the queue is untouched"
          fi
          if gh pr checks "$1" -R "$repo" --required --json bucket --jq '.[].bucket' 2>/dev/null | grep -qx fail; then
            if ! $reran; then
              # `gh run list --status failure` lists completed runs only, while the checks already
              # report a failed job of a run still in progress: the rerun is spent only once a
              # run was rerun, else the next poll looks again.
              n=0
              for id in $(gh run list -R "$repo" --commit "$2" --status failure --json databaseId --jq '.[].databaseId' 2>/dev/null || true); do
                if gh run rerun "$id" -R "$repo" --failed >/dev/null 2>&1; then n=$((n + 1)); else log "warning: could not rerun run $id"; fi
              done
              if [ "$n" -gt 0 ]; then
                log "rerunning the failed checks of train PR #$1 once"
                reran=true settle=1
              fi
            elif [ "$settle" -gt 0 ]; then
              settle=$((settle - 1)) # the rerun may not have replaced the old verdict yet
            else
              die "a required check of train PR #$1 failed again after a rerun: look at it; the queue is untouched"
            fi
          fi ;;
      esac
    fi
    sleep "$poll"
  done
  die "train PR #$1 is not merged after the wait (${ms:-unknown}): look at its checks; the queue is untouched"
}

# adopt_train <PR> <branch> <head sha> <from a fork> <base>: an open train PR this lander did not
# open (another lander that stopped, or a train left to merge by hand). One of the
# maintainer's own (a branch of the repository, its head the one the PR shows) built on the
# current main is merged like the lander's own, rerun rule included; one built on an older
# main is closed, its changes are composed again. 0: dealt with; 1: not touched (a PR whose
# base is not main, a fork's, one whose head moved, or one that could not be closed).
adopt_train() {
  local head cur
  [ "$4" = false ] && [ "$5" = main ] || return 1
  g fetch -q --no-tags origin "+refs/heads/$2:refs/remotes/origin/$2" 2>/dev/null || return 1
  head=$(g rev-parse "refs/remotes/origin/$2" 2>/dev/null) || return 1
  [ "$head" = "$3" ] || return 1
  cur=$(g rev-parse "$main_ref")
  if [ "$(g merge-base "$main_ref" "$head")" != "$cur" ]; then
    log "train PR #$1 is built on a main that moved (not ${cur:0:12}): closing it, composing the train again"
    gh pr close "$1" -R "$repo" --delete-branch >/dev/null 2>&1 || { log "warning: could not close train PR #$1"; return 1; }
    return 0
  fi
  log "adopting the open train PR #$1 ($2), built on the current main"
  base=$cur
  if merge_train "$1" "$head"; then
    log "train PR #$1 merged"
    g push -q origin --delete "$2" >/dev/null 2>&1 || true
  fi
  return 0
}

wt=$state/train
waited=0 fwaited=0 fwaiting=
while :; do
  fetch || die "cannot fetch origin"
  # Main's scripts changed since this lander started (a lander change landed): between two
  # trains, it resets its checkout and runs the new land.sh.
  if [ -z "${LAND_SELF:-}" ] && ! $dry && [ "$here/land.sh" -ef "$src/scripts/land.sh" ] &&
    [ "$(g rev-parse "$main_ref:scripts")" != "$(git -C "$src" rev-parse HEAD:scripts)" ]; then
    log "main's scripts changed: running them (origin/main $(g rev-parse --short=12 "$main_ref"))"
    refresh_src
    [ "$(g rev-parse "$main_ref:scripts")" = "$(git -C "$src" rev-parse HEAD:scripts)" ] ||
      die "the lander's checkout of main, $src, does not hold main's scripts after its reset"
    run_main
  fi
  if ! $dry; then
    # A failed read is not "no open PR": nothing is deleted on it (deleting a PR's head closes it).
    prs_known=true
    others=$(open_trains) || { others= prs_known=false; }
    if [ -n "$others" ]; then
      # shellcheck disable=SC2086  # the five fields of the first line, split on purpose
      if adopt_train $(head -n 1 <<<"$others"); then waited=0; continue; fi
      waited=$((waited + 1))
      [ "$waited" -le "${LAND_OPEN_TRIES:-120}" ] ||
        die "train PR #${others%% *} is still open after the wait and is not one this lander can merge (a fork's, one not based on main, its head moved, or it cannot be closed): look at it; the queue is untouched"
      log "train PR #${others%% *} is open (not one to adopt: another lander's, or a fork's): waiting"
      sleep "$poll"; continue
    fi
    # This machine's trains ended (merged, closed, dropped before their gate) or were left by a
    # lander that died after its push: a branch of one still on origin, with no open PR, goes.
    # $state/branch keeps, one per line, those not cleaned up yet (the PR list failed, a delete
    # failed): never "another machine's" train.
    owns=$(cat "$state/branch" 2>/dev/null || true)
    if [ -n "$owns" ] && $prs_known; then
      keep=
      while read -r own; do
        if [ -z "$own" ] || ! g rev-parse -q --verify "refs/remotes/origin/$own" >/dev/null; then
          :
        elif g push -q origin --delete "$own" >/dev/null 2>&1; then
          log "$own is a train this machine left on origin with no open PR: deleted"
        else
          log "warning: could not delete $own, a train this machine left on origin"
          keep="$keep$own"$'\n'
        fi
      done <<<"$owns"
      if [ -n "$keep" ]; then printf '%s' "$keep" >"$state/branch"; else rm -f "$state/branch"; fi
    fi
    # Another machine's train: deleted when its lander died (its tip older than LAND_TRAIN_STALE),
    # else waited on as an open PR is.
    if $prs_known; then
      while read -r b a; do
        if [ "$a" -ge "${LAND_TRAIN_STALE:-21600}" ]; then
          if g push -q origin --delete "$b" >/dev/null 2>&1; then
            log "$b is a train with no open PR, its tip ${a} s old: its lander died, deleted"
          else
            log "warning: could not delete $b, a train whose lander died"
          fi
        fi
      done < <(foreign_trains "")
    fi
    # Its own count of polls, from the first one on this branch: an earlier wait on a PR or on
    # another train does not shorten it. Without LAND_OPEN_TRIES it lasts until the train goes
    # stale (then deleted above): a dead lander's train stops the next one for no longer.
    # LAND_FORCE=1: a train left by a dead lander does not stall this one until it goes stale.
    foreign=
    [ -n "${LAND_FORCE:-}" ] || foreign=$(young_train "")
    if [ -n "$foreign" ]; then
      [ "$foreign" = "$fwaiting" ] || fwaited=0
      fwaiting=$foreign fwaited=$((fwaited + 1))
      fmax=${LAND_OPEN_TRIES:-$(( ${LAND_TRAIN_STALE:-21600} / (poll > 0 ? poll : 1) + 2 ))}
      [ "$fwaited" -le "$fmax" ] ||
        die "$foreign is still on origin after the wait, with no open PR: another machine's lander gating it, or one that stopped (deleted once its tip is older than LAND_TRAIN_STALE; if its lander is dead: LAND_FORCE=1 make land, or delete $foreign on origin); the queue is untouched"
      log "$foreign is on origin with no open PR: another machine's lander is gating it, waiting (if that lander is dead: LAND_FORCE=1 make land, or delete $foreign on origin)"
      sleep "$poll"; continue
    fi
    fwaiting= waited=0 # each wait starts with its full budget
  fi
  compose
  if [ -z "$TRAIN" ]; then
    [ -n "$(queue)" ] && log "nothing landable: every queued change is held, frozen or in a chain with a rejected change" || log "the queue is empty"
    exit 0
  fi

  # The train worktree, reset to main.
  if [ ! -d "$wt/.git" ] && [ ! -f "$wt/.git" ]; then g worktree add -q --detach "$wt" "$main_ref"; fi
  cd "$wt"
  git cherry-pick --abort >/dev/null 2>&1 || true
  git reset -q --hard && git clean -qfd
  id=$(date -u +%Y%m%d-%H%M%S)
  branch=train/$id
  git checkout -q -B "$branch" "$main_ref"
  $dry || echo "$branch" >>"$state/branch" # this machine's train, until it is cleaned up (never by the exit trap)
  base=$(git rev-parse HEAD) # the main this train is gated on, and the only one it merges onto
  MAP= PEND= WEBC=
  # The newest issue or PR number: pending decisions are numbered above it.
  FLOOR=$(gh api "repos/$repo/issues?state=all&sort=created&direction=desc&per_page=1" --jq '.[0].number // 0' 2>/dev/null || true)
  [[ "$FLOOR" =~ ^[0-9]+$ ]] || FLOOR=0
  landed= commits=() cunit=() cur= skip= upend= uweb=
  # reject_member <slug> <sha> <reason>: a member of the unit being picked is rejected. The unit
  # goes whole: the train is reset to the commit before its first pick, its earlier members leave
  # the train, and only this member moves to failed/; the others stay
  # queued, and that ref blocks their unit until it is queued again.
  reject_member() {
    git cherry-pick --abort >/dev/null 2>&1 || true
    git reset -q --hard "$ustart"
    git clean -qfd # what a partial bundle build left would join the next change
    PEND=$upend WEBC=$uweb landed=$ulanded skip=$cur
    if [ "$un" = 0 ]; then commits=() cunit=(); else commits=("${commits[@]:0:un}") cunit=("${cunit[@]:0:un}"); fi
    if [ "${U_SIZE[cur]}" = 1 ]; then reject "$1" "$2" "$3"; else reject "$1" "$2" "$3 ($(unit_name "$cur" | sed 's/^the //'))"; fi
  }
  while read -r s sha u; do
    if [ "$u" != "$cur" ]; then
      cur=$u ustart=$(git rev-parse HEAD) upend=$PEND uweb=$WEBC ulanded=$landed un=${#commits[@]}
    fi
    [ "$u" != "$skip" ] || continue
    # A member main already holds is dequeued; its unit goes on without it.
    if landed_on_main "$s" "$sha"; then
      log "$s is already on main (a commit there carries its Lander-Slug and Lander-Tip): dequeued"
      $dry || dequeue "$s" "$sha" || log "warning: could not delete ready/$s"
      # A rejection left from an earlier version would block a later change queued AFTER=<slug>.
      $dry || g push -q origin --delete "failed/$s" >/dev/null 2>&1 || true
      continue
    fi
    if ! git cherry-pick --allow-empty "$sha" >/dev/null 2>&1 && ! bundle_pick "$s"; then
      git cherry-pick --abort >/dev/null 2>&1 || true
      git reset -q --hard
      # An edit of a pending decision whose first change landed numbered: applied to D<n>-<slug>.md.
      if remap_pick "$sha"; then
        log "$s edits a pending decision that main holds numbered: applied to the numbered file"
      else
        # Its train landed and the queue entry outlived it: the same diff is on main.
        if [ "$(g cherry "$main_ref" "$sha" "$sha^" 2>/dev/null | cut -c1)" = - ]; then
          log "$s is already on main: dequeued"
          $dry || dequeue "$s" "$sha" || log "warning: could not delete ready/$s"
          $dry || g push -q origin --delete "failed/$s" >/dev/null 2>&1 || true
          continue
        fi
        hint= # grep without -q reads all of git log: an early exit would fail it under pipefail
        if g log --format=%s "$sha^..$main_ref" | grep -xF "$(title "$sha")" >/dev/null; then
          hint=" (main already holds a commit with its title: landed, its decision numbered?)"
        fi
        reject_member "$s" "$sha" "conflict: it does not apply on main plus the changes before it in the train$hint"
        continue
      fi
    fi
    tip=$(trailer Lander-Tip "$sha")
    review=$(review_of "$sha")
    # Every `Review` line goes, with or without the space and in any case (`review :x` is a
    # trailer to git too): the record is the one value read above.
    msg=$(git log -1 --format=%B | grep -vE '^Lander-[A-Za-z]+: ' | grep -viE '^review[[:space:]]*:' || true)
    # The lander's other trailers stay off main; the slug and the branch head ready.sh
    # squashed stay, so `make worktree-clean` can tell that the work of a feat/<slug>
    # worktree landed (the train PR is not its PR), and so does the review record, readable
    # with `git log` (it was only in the train PR body). Any copy in the message was dropped
    # above: the slug is this lander's, the tip the queued commit's trailer, the review the
    # last one of the queued commit (review_of).
    targs=(--trailer "Lander-Slug: $s")
    [ -z "$tip" ] || targs+=(--trailer "Lander-Tip: $tip")
    [ -z "$review" ] || targs+=(--trailer "Review: $review")
    printf '%s\n' "$msg" | git interpret-trailers "${targs[@]}" | git commit -q --amend --allow-empty -F -
    pending_dup # a rejected unit leaves nothing behind for the later ones (reject_member)
    if [ -n "$DUP" ]; then
      reject_member "$s" "$sha" "$DUP"
      continue
    fi
    # The queued diff adds a citation under web/ (the files numbering rewrites only, not _test.go
    # or testdata/): its bundle is built again after the numbering. Read from the queued commit,
    # not from what the numbering rewrites: remap_pick has already rewritten a landed citation.
    # grep without -q reads all of the diff: an early exit would SIGPIPE git show under pipefail.
    if git show "$sha" -- web/ ':(exclude)*_test.go' ':(exclude)*/testdata/*' | grep '^+.*D-new-' >/dev/null; then
      WEBC="$WEBC ${#commits[@]}"
    fi
    landed=${landed:+$landed$'\n'}"$s $sha"
    commits+=("$(git rev-parse HEAD)") cunit+=("$u")
  done <<<"$TRAIN"
  k=${#commits[@]}
  # After the picks: the citation check, then the numbers, once for the whole train. A rejection
  # here leaves the worktree as it is: the queue is composed again without the unit's member.
  if [ "$k" != 0 ]; then
    citation_check
    if [ -n "$BAD" ]; then
      while IFS=' ' read -r i where; do
        reject_at "$i" "it cites a pending decision that is neither on main nor earlier in the train: $where"
      done <<<"$BAD"
      if $dry; then log "dry run: the train would be composed again without the rejected changes"; exit 0; fi
      continue
    fi
    number_train
    if [ -n "$BUILD_FAIL" ]; then
      l=$(sed -n "$((BUILD_FAIL + 1))p" <<<"$landed")
      reject_at "$BUILD_FAIL" "it cites a pending decision under web/ and the bundle could not be rebuilt (see $state/bundle-${l%% *}.log)"
      if $dry; then log "dry run: the train would be composed again without the rejected change"; exit 0; fi
      continue
    fi
  fi
  if [ "$k" = 0 ]; then
    # A dry run moves nothing out of the queue: composing again would find the same train.
    if $dry; then log "dry run: no queued change applies on main"; exit 0; fi
    continue
  fi
  log "train $branch: $k change(s): $(cut -d' ' -f1 <<<"$landed" | tr '\n' ' ' | sed 's/ $//') [gate: $gate_mode]"
  if $dry; then
    git log --oneline "$main_ref..HEAD"
    cd "$main_repo"; g worktree remove --force "$wt" >/dev/null 2>&1 || true
    exit 0
  fi

  # A train another machine pushed since the loop top: its lander is gating it, so this
  # composition is dropped before anything is pushed, and the loop top waits on it.
  g fetch -q --no-tags --prune origin '+refs/heads/train/*:refs/remotes/origin/train/*' ||
    die "cannot fetch origin before the gate of $branch: the queue is untouched"
  foreign=
  [ -n "${LAND_FORCE:-}" ] || foreign=$(young_train "")
  if [ -n "$foreign" ]; then
    log "$foreign appeared on origin while $branch was composed: another machine's lander is gating it, composing again after it"
    git checkout -q --detach && git branch -q -D "$branch" || true
    continue
  fi

  # The gate: once; on red, again (flaky?) unless the signoff's targeted retry already
  # confirmed the failure (D1755); then bisection over prefixes.
  glog=$state/gate-$id.log
  rc=0; gate "$glog" || rc=$?
  [ "$rc" != 2 ] || die "the gate of $branch failed for a reason that is not a test (see $glog): the queue is untouched"
  [ "$rc" != 0 ] || flaky_issue "$glog" "failed and passed on a targeted retry of the signoff"
  if [ "$rc" = 1 ]; then
    fails=$(failing "$glog")
    fails_lines=$(failing "$glog" lines)
    rc2=1
    if grep -q '^signoff: confirmed on a targeted retry:' "$glog"; then
      log "red, and red again on the signoff's targeted retry of${fails:+ $fails}: not a flake"
    else
      rc2=0; gate "$glog.2" || rc2=$?
      [ "$rc2" != 2 ] || die "the gate of $branch failed for a reason that is not a test (see $glog.2): the queue is untouched"
    fi
    if [ "$rc2" = 0 ]; then
      log "red once, green on the re-run: a flaky test (${fails:-not named})"
      # The e2e runner's own lines say which assertion failed first: the issue keeps them (#1952).
      issue_body="The gate of the train \`$branch\` failed, then passed on the same tree. Failing tests: ${fails:-none named in the log}. A re-run is not a fix: find why it fails."
      e2e_lines=$(e2e_report "$glog")
      if [ -n "$e2e_lines" ]; then
        issue_body="$issue_body"$'\n\n'"The first failing line of each e2e scenario:"$'\n\n'"$e2e_lines"
      fi
      issue_flaky "${fails_lines:-the gate of a train}" "failed once and passed on a re-run" "$issue_body"
    else
      # The prefixes end at a unit's boundary: ends[m] is how many changes the first m units
      # hold, so a probe or a gate runs on a unit's last commit and no prefix splits a unit.
      ends=(0)
      for ((i = 1; i < k; i++)); do [ "${cunit[i]}" = "${cunit[i - 1]}" ] || ends+=("$i"); done
      ends+=("$k")
      nu=$((${#ends[@]} - 1))
      lo=0 hi=$nu good=
      # The confirmed tests alone (their packages, a minute or two) find the change that
      # breaks them; the green prefix still gets its full gate below. Without a confirmed
      # retry, or with its machine gone, each prefix gets a full gate.
      if [ -e "$glog.2" ]; then probe=$(probe_of "$glog.2" "$gate_token"); else probe=$(probe_of "$glog" "$gate_token"); fi
      if [ -n "$probe" ]; then
        log "bisecting with the confirmed tests only: $probe"
        while [ $((hi - lo)) -gt 1 ]; do
          m=$(( (lo + hi) / 2 )) c=${ends[m]}
          git reset -q --hard "${commits[c - 1]}"
          r=0
          # shellcheck disable=SC2086  # the probe is three words on purpose, checked by probe_of
          (cd "$wt" && "$here/signoff.sh" --probe $probe) >>"$glog.probe" 2>&1 || r=$?
          if [ "$r" = 75 ]; then
            log "the probe's machine is unavailable: bisecting with full gates"
            probe= lo=0 hi=$nu
            break
          fi
          if [ "$r" = 0 ]; then lo=$m; else hi=$m; fi
        done
      fi
      if [ -z "$probe" ]; then
        while [ $((hi - lo)) -gt 1 ]; do
          m=$(( (lo + hi) / 2 )) c=${ends[m]}
          git reset -q --hard "${commits[c - 1]}"
          r=0; gate "$glog.p$m" || r=$?
          [ "$r" != 2 ] || die "the gate of a prefix of $branch failed for a reason that is not a test: the queue is untouched"
          if [ "$r" = 0 ]; then lo=$m; good=$(git rev-parse HEAD); else hi=$m; fi
        done
      fi
      # The first red unit: a unit of one is rejected as a change; of a longer one, its last tip
      # in queue order (the last member picked, if main held that tip), and the rest stays queued.
      first=${ends[hi - 1]} c=${ends[hi]}
      slice=$(sed -n "$((first + 1)),${c}p" <<<"$landed")
      if [ $((c - first)) = 1 ]; then
        bad=$slice
        reject "${bad%% *}" "${bad#* }" "the train gate is red with it and green without it (tests: ${fails:-see the gate log})"
      else
        bu=${cunit[first]}
        bad=$(grep -xF "${U_TIP[bu]}" <<<"$slice" || tail -n 1 <<<"$slice")
        reject "${bad%% *}" "${bad#* }" "the train gate is red with the chain ${slice%% *}..${bad%% *} and green without it (tests: ${fails:-see the gate log})"
      fi
      if [ "$lo" = 0 ]; then
        g push -q origin --delete "$branch" >/dev/null 2>&1 || true
        continue
      fi
      if [ -z "$good" ]; then
        # Found by the probe: the prefix before the rejected unit lands on a full gate.
        c=${ends[lo]}
        git reset -q --hard "${commits[c - 1]}"
        r=0; gate "$glog.p$lo" || r=$?
        [ "$r" != 2 ] || die "the gate of a prefix of $branch failed for a reason that is not a test: the queue is untouched"
        if [ "$r" != 0 ]; then
          log "the prefix before ${bad%% *} is red too: composing the train again"
          g push -q origin --delete "$branch" >/dev/null 2>&1 || true
          continue
        fi
        good=$(git rev-parse HEAD)
      fi
      git reset -q --hard "$good"
      g push -q --force origin "$good:refs/heads/$branch"
      k=${ends[lo]}
      landed=$(head -n "$k" <<<"$landed")
    fi
  fi

  # Main moved during the gate (a contributor's PR, a Dependabot merge, the release): the
  # gated tree is not the one a merge would land. Compose the train again on the new main.
  g fetch -q --no-tags origin main || die "cannot fetch origin after the gate of $branch: the queue is untouched"
  if [ "$(g rev-parse "$main_ref")" != "$base" ]; then
    log "main moved during the gate of $branch (${base:0:12}..$(g rev-parse --short=12 "$main_ref")): composing the train again"
    g push -q origin --delete "$branch" >/dev/null 2>&1 || true
    continue
  fi

  # One PR for the train, rebase-merged: each change stays its own commit on main. Its third
  # line says which gate the train had, read from the signoff status of the gated head.
  gline=$(gate_line "$(git rev-parse HEAD)")
  case "$gline" in "Gate: unknown"*) log "warning: $gline" ;; *) log "$gline" ;; esac
  body=$state/body-$id.md
  {
    echo "Size exception: a train of $k change(s), each under the cap when it was queued (make ready)."
    echo
    echo "$gline"
    echo
    echo "Landed by scripts/land.sh after one gate of the whole train (the \`signoff\` status of the head)."
    while read -r s sha; do
      echo
      echo "### \`$s\`: $(title "$sha" | sed 's/^/`/; s/$/`/')"
      echo
      echo "- review: $(r=$(review_of "$sha"); echo "${r:-not recorded}")"
      g log -1 --format=%B "$sha" | grep -iE '^(closes|refs|fixes) #[0-9]+' | sed 's/^/- /' || true
    done <<<"$landed"
  } >"$body"
  url=$(gh pr create -R "$repo" --base main --head "$branch" --title "chore(dev): train $id, $k change(s)" --body-file "$body")
  num=${url##*/}
  log "train PR #$num opened: $url"
  if ! merge_train "$num" "$(git rev-parse HEAD)"; then
    git checkout -q --detach && git branch -q -D "$branch" || true
    continue
  fi
  log "train PR #$num merged"
  git checkout -q --detach && git branch -q -D "$branch" || true
  g push -q origin --delete "$branch" >/dev/null 2>&1 || true
  stuck=
  while read -r s sha; do
    dequeue "$s" "$sha" || stuck="$stuck $s"
    g push -q origin --delete "failed/$s" >/dev/null 2>&1 || true
  done <<<"$landed"
  [ -z "$stuck" ] || die "train PR #$num landed, but the queue entries of$stuck could not be deleted (network?): delete them with git push origin :refs/heads/ready/<slug> before the lander runs again"
done
