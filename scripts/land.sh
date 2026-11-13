#!/usr/bin/env bash
# land.sh — the lander: lands the maintainer's queued changes on main in trains (D1706).
#
#   land.sh            land trains until the queue holds nothing landable, then exit
#   land.sh --daemon   the same, detached (nohup, log in the state dir), unless one runs
#   land.sh --status   the queue, the open train PR and the rejected changes
#   land.sh --dry-run  compose the next train and print it: nothing gated, pushed or opened
#
# The queue is `refs/heads/ready/<slug>` on origin (scripts/ready.sh pushes them), oldest
# first. A train is main plus at most LAND_MAX (8) changes, cherry-picked in queue order
# after their `Lander-After:` change; a held change (`Lander-Hold: yes`), one waiting on a
# held or rejected change, and while the freeze is open (scripts/freeze-open.sh, D1384) a
# feat/perf/refactor or breaking change stay queued. Each pending decision a change adds
# (`docs/decisions/D-new-<slug>.md`) gets the next number above both the highest on the
# tree and the newest issue or PR number (a contributor's decision takes its PR's number,
# D672: numbers above the counter keep the two apart and in time order), and every
# `D-new-<slug>` citation is rewritten, in the change's own commit. A citation of one whose
# change already landed (main holds `D<n>-<slug>.md`) reads as `D<n>`; an edit of its
# `D-new-<slug>.md` is applied to `D<n>-<slug>.md`; a change adding a `D-new-<slug>.md` whose
# slug main holds is rejected, naming the file.
#
# The train branch `train/<UTC time>` is gated by scripts/signoff.sh (the gate, the web
# legs and the Windows leg when needed, the CI fallback on exit 75, the push and the
# `signoff` status: one gate for the whole train). With SIGNOFF_HOSTED=1, the maintainer's
# setting (D2040), the gate is ci.yml on GitHub: every leg main's CI runs,
# Windows and macOS included, in parallel; a prefix of a bisection is gated the same way. The signoff itself retries once the
# Go tests a red run names (its step 4c): green, the train lands on that gate and a `bug`
# issue names them; red, the failure is confirmed and bisection starts without a re-run.
# A signoff that exits 69 (ssh or rsync dropped, so no test failed: #1843; its line `the build
# host failed (exit N) before the gate finished` is for people: the log holds the change's own
# output, so the code decides) is not red: the same tree is gated again, no flaky
# issue, up to LAND_INFRA_TRIES gates in a row; a host that stays down stops the lander with
# the queue untouched. Any other red: one re-run of the same tree; green then means a flaky
# test, so the train lands and a `bug` issue names the tests. Red twice: bisection over the
# train's prefixes finds the first failing change, which is rejected, and the green prefix
# before it lands; when the signoff confirmed the failure on named Go tests, each prefix runs only those
# tests' packages (`signoff.sh --probe`, D1878) and the green prefix then gets
# one full gate before it lands. Green: one PR, title
# `chore(dev): train <id>, <k> changes`, rebase-merged so every change stays its own
# conventional commit on main; once merged, the landed queue entries are deleted (a queued
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
# break and the frontend's unit tests, the legs `make check` does not run. A train of several
# changes takes only changes whose pre-check is green (or has no verdict: none started within
# LAND_PRECHECK_START, still running past LAND_PRECHECK_WAIT, cancelled, or GitHub did not
# answer). When none is, the oldest change whose pre-check is still running is gated alone,
# else the oldest red one: the build host never idles waiting on GitHub, and a change that
# breaks a leg costs one gate of its own instead of a red train and its bisection.
#
# A rejected change moves to `refs/heads/failed/<slug>`: a commit on top of it whose
# message is the reason, readable from any clone (`git log -1 origin/failed/<slug>`).
# One lander at a time: per machine a lock in the state dir, across machines an open PR
# whose head is `train/*`. A lander that finds one of the repository's own built on the current
# main adopts it (the same merge, the same rerun-once rule) and goes on with the queue; one
# built on an older main is closed and its changes composed again; a fork's, or one whose
# head it cannot match, is waited on for LAND_OPEN_TRIES polls, then the lander stops.
#
#   LAND_INFRA_TRIES (3)  LAND_INFRA_WAIT (120 s)
#   LAND_MAX (8)  LAND_POLL (30 s)  LAND_MERGE_TRIES (90 polls)  LAND_OPEN_TRIES (120 polls)  LAND_STATE (state dir)
#   LAND_PRECHECK_START (300 s)  LAND_PRECHECK_WAIT (1200 s), from the queued commit's time
#   SIGNOFF_* are passed to signoff.sh unchanged.
#
# Plain bash 3.2 (the macOS default): no associative arrays.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
log() { echo "land: $(date -u +%H:%M:%S) $*" >&2; }
die() { log "$*"; exit 1; }

common=$(git -C "$here" rev-parse --path-format=absolute --git-common-dir)
main_repo=${common%/.git}
key=$(printf %s "$main_repo" | cksum | cut -d' ' -f1)
state=${LAND_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/cartographer-lander/$key}
mkdir -p "$state"
max=${LAND_MAX:-8}
poll=${LAND_POLL:-30}

repo=$(cd "$main_repo" && "$here/upstream-repo.sh")
g() { git -C "$main_repo" "$@"; }

running() { [ -f "$state/pid" ] && kill -0 "$(cat "$state/pid")" 2>/dev/null; }

case "${1:-}" in
  --daemon)
    if running; then echo "land: a lander already runs here (pid $(cat "$state/pid"))"; exit 0; fi
    nohup "$0" >>"$state/log" 2>&1 </dev/null &
    echo "land: lander started (pid $!), log $state/log"
    exit 0 ;;
esac

fetch() { g fetch -q --prune origin main '+refs/heads/ready/*:refs/remotes/origin/ready/*' '+refs/heads/failed/*:refs/remotes/origin/failed/*'; }

# The queue, oldest first: "<slug> <sha>" lines.
queue() { g for-each-ref --sort=committerdate --format='%(refname:lstrip=4) %(objectname)' refs/remotes/origin/ready/; }
trailer() { g log -1 --format=%B "$2" | git interpret-trailers --parse | sed -n "s/^$1: *//p" | head -n 1; }
title() { g log -1 --format=%s "$1"; }
# "<number> <branch> <head sha> <true if from a fork> <base branch>" per open train PR.
open_trains() { gh pr list -R "$repo" --state open --json number,headRefName,headRefOid,isCrossRepository,baseRefName --jq '.[] | select(.headRefName | startswith("train/")) | "\(.number) \(.headRefName) \(.headRefOid) \(.isCrossRepository) \(.baseRefName)"'; }

if [ "${1:-}" = --status ]; then
  fetch || true
  echo "queue:"; queue | while read -r s sha; do
    extra=; a=$(trailer Lander-After "$sha"); [ -z "$a" ] || extra=" (after $a)"
    [ -z "$(trailer Lander-Hold "$sha")" ] || extra="$extra (held)"
    echo "  $s: $(title "$sha")$extra"
  done
  echo "open train PR:"; open_trains | cut -d' ' -f1,2 | sed 's/^/  #/' || true
  echo "rejected:"; g for-each-ref --format='%(refname:lstrip=4) %(contents:subject)' refs/remotes/origin/failed/ | sed 's/^/  /'
  [ ! -s "$state/blocked" ] || echo "stopped: $(cat "$state/blocked")"
  if running; then echo "lander: running (pid $(cat "$state/pid")), log $state/log"; else echo "lander: not running"; fi
  exit 0
fi
dry=false
[ "${1:-}" != --dry-run ] || dry=true

# One lander per machine and repository.
if ! $dry; then
  if running && [ "$(cat "$state/pid")" != $$ ]; then echo "land: a lander already runs here"; exit 0; fi
  echo $$ >"$state/pid"
  rm -f "$state/blocked"
  # The root this lander runs from: removing that worktree deletes the scripts under it
  # (scripts/lander-guard.sh, used by worktree-rm and worktree-clean, reads this file).
  (cd "$here/.." && pwd -P) >"$state/root"
  trap 'rm -f "$state/pid" "$state/root"' EXIT
  # The lander mostly waits (on the gate, on GitHub). A Mac on battery sleeps when idle, and
  # asleep the lander advances only in its dark wakes, a quarter of an hour apart, waking
  # with the network still down: caffeinate keeps it awake (not a closed lid) until the
  # lander exits.
  if command -v caffeinate >/dev/null 2>&1; then caffeinate -i -w $$ >/dev/null 2>&1 </dev/null & fi
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

# reject <slug> <sha> <reason>: the change leaves the queue for failed/<slug>.
reject() {
  log "rejected $1: $3"
  $dry && return 0
  local f
  f=$(git -C "$main_repo" commit-tree "$2^{tree}" -p "$2" -m "lander: $3" -m "Fix it on the branch and run make ready again; this ref is replaced then.")
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
  for c in $(g log origin/main --format=%H --fixed-strings --grep="Lander-Slug: $1"); do
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

# compose: selects the train into TRAIN (lines "<slug> <sha>"), in landing order: the
# changes whose pre-check is green, else one change alone (see the header).
compose() {
  local q freeze=false failed s sha
  q=$(queue)
  TRAIN=
  [ -n "$q" ] || return 0
  if GH_REPO=$repo "$here/freeze-open.sh" >/dev/null 2>&1; then freeze=true; fi
  failed=$(g for-each-ref --format='%(refname:lstrip=4)' refs/remotes/origin/failed/)
  VERDICTS=
  while read -r s sha; do
    [ -n "$s" ] || continue
    VERDICTS=$VERDICTS"$s $(precheck "$sha")"$'\n'
  done <<<"$q"
  pick ok "$max"
  [ -z "$TRAIN" ] || return 0
  pick wait 1
  if [ -n "$TRAIN" ]; then
    log "${TRAIN%% *}: its hosted pre-check is still running and nothing else is ready: gated alone"
    return 0
  fi
  pick red 1
  [ -z "$TRAIN" ] || log "${TRAIN%% *}: its hosted pre-check is red: gated alone"
}

# pick <verdict> <n>: adds to TRAIN, in queue order and after their Lander-After change, up
# to n changes in all whose pre-check verdict is <verdict>. Reads compose's q, freeze, failed.
pick() {
  local pass changed s sha after
  for pass in 1 2 3 4 5 6 7 8; do
    changed=false
    while read -r s sha; do
      [ -n "$s" ] || continue
      grep -qx "$s $sha" <<<"$TRAIN" && continue
      [ "$(printf '%s\n' "$TRAIN" | grep -c .)" -lt "$2" ] || break
      grep -qx "$s $1" <<<"$VERDICTS" || continue
      [ -z "$(trailer Lander-Hold "$sha")" ] || continue
      if $freeze && breaking "$sha"; then continue; fi
      after=$(trailer Lander-After "$sha")
      if [ -n "$after" ]; then
        # Waits while the change it follows is queued and not yet in this train, or rejected.
        if grep -q "^$after " <<<"$q" && ! grep -q "^$after " <<<"$TRAIN"; then continue; fi
        if grep -qx "$after" <<<"$failed"; then continue; fi
      fi
      TRAIN=${TRAIN:+$TRAIN$'\n'}"$s $sha"
      changed=true
    done <<<"$q"
    $changed || break
  done
}

# bundle_pick <slug>: in a cherry-pick stopped on conflicts, those in internal/webui/dist only
# (the committed Atlas bundle, rebuilt whole by every frontend change: two of them always
# conflict there, never in web/src) are resolved by building the bundle again from the merged
# sources, and the pick goes on (D2040). The gate's TestBundleMatchesSource
# proves the result. 1 when another path conflicts, Node is missing, or the build fails: the
# change is then rejected as a conflict, as before. LAND_BUNDLE_BUILD replaces `make -s web`.
bundle_pick() {
  local u blog
  u=$(git diff --name-only --diff-filter=U 2>/dev/null) || return 1
  [ -n "$u" ] || return 1
  if grep -qv '^internal/webui/dist/' <<<"$u"; then return 1; fi
  if [ -z "${LAND_BUNDLE_BUILD:-}" ] && ! command -v node >/dev/null 2>&1; then
    log "$1: only the Atlas bundle conflicts, but Node is not installed here: not rebuilt"
    return 1
  fi
  blog=$state/bundle-$1.log
  if ! ${LAND_BUNDLE_BUILD:-make -s web} >"$blog" 2>&1; then
    log "$1: only the Atlas bundle conflicts, and building it again failed (see $blog)"
    return 1
  fi
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
# number_decisions rewrites a citation. 1 when no edit is of that kind or the patch does not apply.
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
    PH=$ph NUM=${pair#*=} perl -pi -e 'BEGIN { ($s = $ENV{PH}) =~ s/^D-new-//; } $t = /^diff --git .*_test\.go$/ if /^diff --git /; next if $t; s/\Q$ENV{PH}\E(?=\.md(?![a-z0-9-]))/$ENV{NUM}-$s/g; s/\Q$ENV{PH}\E(?![a-z0-9-])/$ENV{NUM}/g' "$patch"
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

# number_decisions: in the train worktree, after a cherry-pick, numbers the pending decisions
# the commit adds and rewrites the known placeholders (MAP: "<placeholder>=D<n>" words) in
# the files the commit touches. Text already on main is never rewritten, and test files
# are left alone (fixtures may spell a placeholder). LEFT is set to a placeholder the
# commit still cites that is neither on main nor earlier in the train. A citation of a
# pending decision that main holds as D<n>-<slug>.md (its change landed in an earlier train) reads
# as D<n>; a pending decision whose slug main already holds sets DUP to the reason. Not run in a
# subshell: MAP carries the numbers to the later changes of the train.
number_decisions() {
  local f ph n pair num touched landed
  DUP=
  touched=$(git diff-tree --no-commit-id --name-only -r HEAD | grep -v '_test\.go$' || true)
  for f in $(grep -E '^docs/decisions/D-new-[a-z0-9-]+\.md$' <<<"$touched" || true); do
    [ -f "$f" ] || continue
    ph=$(basename "$f" .md)
    landed=$(landed_decision "${ph#D-new-}")
    if [ -n "$landed" ]; then
      DUP="it adds $f, but main already holds docs/decisions/$landed-${ph#D-new-}.md (two decisions with one slug)"
      return 0
    fi
    case " $MAP " in *" $ph="*)
      DUP="it adds $f, but an earlier change of the train added a decision with that slug"
      return 0 ;;
    esac
    n=$(ls docs/decisions | sed -nE 's/^D([0-9]+)-.*/\1/p' | sort -n | tail -n 1)
    n=${n:-0}
    [ "${FLOOR:-0}" -le "$n" ] || n=$FLOOR
    n=$((n + 1))
    git mv "$f" "docs/decisions/D$n-${ph#D-new-}.md"
    touched=$(printf '%s\n' "$touched" | sed "s#^$f\$#docs/decisions/D$n-${ph#D-new-}.md#")
    MAP="$MAP $ph=D$n"
  done
  # A citation of a pending decision that main holds numbered: the same rewrite.
  while IFS= read -r ph; do
    [ -n "$ph" ] || continue
    case " $MAP " in *" $ph="*) continue ;; esac
    landed=$(landed_decision "${ph#D-new-}")
    [ -z "$landed" ] || MAP="$MAP $ph=$landed"
  done < <(while IFS= read -r f; do
    [ -n "$f" ] && [ -f "$f" ] || continue
    grep -ohE 'D-new-[a-z0-9][a-z0-9-]*' "$f" || true
  done <<<"$touched" | sort -u)
  for pair in $MAP; do
    ph=${pair%%=*} num=${pair#*=}
    while IFS= read -r f; do
      [ -n "$f" ] && [ -f "$f" ] || continue
      # The file name D-new-<slug>.md becomes D<n>-<slug>.md, so the path form (a link) is
      # rewritten first; then the bare word. A placeholder is a whole word: the one for
      # slug `a` must not rewrite the one for `ab`.
      PH=$ph NUM=$num perl -pi -e 'BEGIN { ($s = $ENV{PH}) =~ s/^D-new-//; } s/\Q$ENV{PH}\E(?=\.md(?![a-z0-9-]))/$ENV{NUM}-$s/g; s/\Q$ENV{PH}\E(?![a-z0-9-])/$ENV{NUM}/g' "$f"
    done <<<"$touched"
  done
  git add -A
  git diff --cached --quiet || git commit -q --amend --no-edit --allow-empty
  LEFT=$(while IFS= read -r f; do
    [ -n "$f" ] && [ -f "$f" ] || continue
    grep -noE 'D-new-[a-z0-9][a-z0-9-]*' "$f" | sed "s#^#$f:#" | head -n 1
  done <<<"$touched" | head -n 1 || true) # grep finds nothing: pipefail would end the lander
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
issue_flaky() { # $1 = names, one per line, $2 = what happened, $3 = body
  gh issue create -R "$repo" --label bug --title "$(issue_title "$1" "$2")" --body "$3" >/dev/null ||
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
    if g fetch -q origin main 2>/dev/null; then
      cur=$(g rev-parse origin/main)
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
  g fetch -q origin "+refs/heads/$2:refs/remotes/origin/$2" 2>/dev/null || return 1
  head=$(g rev-parse "refs/remotes/origin/$2" 2>/dev/null) || return 1
  [ "$head" = "$3" ] || return 1
  cur=$(g rev-parse origin/main)
  if [ "$(g merge-base origin/main "$head")" != "$cur" ]; then
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
waited=0
while :; do
  fetch || die "cannot fetch origin"
  if ! $dry; then
    others=$(open_trains || true)
    if [ -n "$others" ]; then
      # shellcheck disable=SC2086  # the five fields of the first line, split on purpose
      if adopt_train $(head -n 1 <<<"$others"); then waited=0; continue; fi
      waited=$((waited + 1))
      [ "$waited" -le "${LAND_OPEN_TRIES:-120}" ] ||
        die "train PR #${others%% *} is still open after the wait and is not one this lander can merge (a fork's, one not based on main, its head moved, or it cannot be closed): look at it; the queue is untouched"
      log "train PR #${others%% *} is open (not one to adopt: another lander's, or a fork's): waiting"
      sleep "$poll"; continue
    fi
  fi
  compose
  if [ -z "$TRAIN" ]; then
    [ -n "$(queue)" ] && log "nothing landable: every queued change is held, frozen or waits on another" || log "the queue is empty"
    exit 0
  fi

  # The train worktree, reset to main.
  if [ ! -d "$wt/.git" ] && [ ! -f "$wt/.git" ]; then g worktree add -q --detach "$wt" origin/main; fi
  cd "$wt"
  git cherry-pick --abort >/dev/null 2>&1 || true
  git reset -q --hard && git clean -qfd
  id=$(date -u +%Y%m%d-%H%M%S)
  branch=train/$id
  git checkout -q -B "$branch" origin/main
  base=$(git rev-parse HEAD) # the main this train is gated on, and the only one it merges onto
  MAP=
  # The newest issue or PR number: pending decisions are numbered above it.
  FLOOR=$(gh api "repos/$repo/issues?state=all&sort=created&direction=desc&per_page=1" --jq '.[0].number // 0' 2>/dev/null || true)
  [[ "$FLOOR" =~ ^[0-9]+$ ]] || FLOOR=0
  landed= commits=()
  while read -r s sha; do
    if landed_on_main "$s" "$sha"; then
      log "$s is already on main (a commit there carries its Lander-Slug and Lander-Tip): dequeued"
      $dry || dequeue "$s" "$sha" || log "warning: could not delete ready/$s"
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
        if [ "$(g cherry origin/main "$sha" "$sha^" 2>/dev/null | cut -c1)" = - ]; then
          log "$s is already on main: dequeued"
          $dry || dequeue "$s" "$sha" || log "warning: could not delete ready/$s"
          continue
        fi
        hint= # grep without -q reads all of git log: an early exit would fail it under pipefail
        if g log --format=%s "$sha^..origin/main" | grep -xF "$(title "$sha")" >/dev/null; then
          hint=" (main already holds a commit with its title: landed, its decision numbered?)"
        fi
        reject "$s" "$sha" "conflict: it does not apply on main plus the changes before it in the train$hint"
        continue
      fi
    fi
    tip=$(trailer Lander-Tip "$sha")
    msg=$(git log -1 --format=%B | grep -vE '^(Lander-[A-Za-z]+|Review): ' || true)
    # The lander's other trailers stay off main; the slug and the branch head ready.sh
    # squashed stay, so `make worktree-clean` can tell that the work of a feat/<slug>
    # worktree landed (the train PR is not its PR). Any copy in the message was dropped above.
    targs=(--trailer "Lander-Slug: $s")
    [ -z "$tip" ] || targs+=(--trailer "Lander-Tip: $tip")
    printf '%s\n' "$msg" | git interpret-trailers "${targs[@]}" | git commit -q --amend --allow-empty -F -
    premap=$MAP # a rejected change leaves no number behind for the later ones
    number_decisions
    if [ -n "$DUP" ]; then
      MAP=$premap
      git reset -q --hard HEAD~1
      reject "$s" "$sha" "$DUP"
      continue
    fi
    if [ -n "$LEFT" ]; then
      MAP=$premap
      git reset -q --hard HEAD~1
      reject "$s" "$sha" "it cites a pending decision that is neither on main nor earlier in the train: $LEFT"
      continue
    fi
    landed=${landed:+$landed$'\n'}"$s $sha"
    commits+=("$(git rev-parse HEAD)")
  done <<<"$TRAIN"
  k=${#commits[@]}
  if [ "$k" = 0 ]; then
    # A dry run moves nothing out of the queue: composing again would find the same train.
    if $dry; then log "dry run: no queued change applies on main"; exit 0; fi
    continue
  fi
  log "train $branch: $k change(s): $(cut -d' ' -f1 <<<"$landed" | tr '\n' ' ')"
  if $dry; then
    git log --oneline "origin/main..HEAD"
    cd "$main_repo"; g worktree remove --force "$wt" >/dev/null 2>&1 || true
    exit 0
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
      lo=0 hi=$k good=
      # The confirmed tests alone (their packages, a minute or two) find the change that
      # breaks them; the green prefix still gets its full gate below. Without a confirmed
      # retry, or with its machine gone, each prefix gets a full gate.
      if [ -e "$glog.2" ]; then probe=$(probe_of "$glog.2" "$gate_token"); else probe=$(probe_of "$glog" "$gate_token"); fi
      if [ -n "$probe" ]; then
        log "bisecting with the confirmed tests only: $probe"
        while [ $((hi - lo)) -gt 1 ]; do
          m=$(( (lo + hi) / 2 ))
          git reset -q --hard "${commits[m - 1]}"
          r=0
          # shellcheck disable=SC2086  # the probe is three words on purpose, checked by probe_of
          (cd "$wt" && "$here/signoff.sh" --probe $probe) >>"$glog.probe" 2>&1 || r=$?
          if [ "$r" = 75 ]; then
            log "the probe's machine is unavailable: bisecting with full gates"
            probe= lo=0 hi=$k
            break
          fi
          if [ "$r" = 0 ]; then lo=$m; else hi=$m; fi
        done
      fi
      if [ -z "$probe" ]; then
        while [ $((hi - lo)) -gt 1 ]; do
          m=$(( (lo + hi) / 2 ))
          git reset -q --hard "${commits[m - 1]}"
          r=0; gate "$glog.p$m" || r=$?
          [ "$r" != 2 ] || die "the gate of a prefix of $branch failed for a reason that is not a test: the queue is untouched"
          if [ "$r" = 0 ]; then lo=$m; good=$(git rev-parse HEAD); else hi=$m; fi
        done
      fi
      bad=$(sed -n "${hi}p" <<<"$landed")
      reject "${bad%% *}" "${bad#* }" "the train gate is red with it and green without it (tests: ${fails:-see the gate log})"
      if [ "$lo" = 0 ]; then
        g push -q origin --delete "$branch" >/dev/null 2>&1 || true
        continue
      fi
      if [ -z "$good" ]; then
        # Found by the probe: the prefix before the rejected change lands on a full gate.
        git reset -q --hard "${commits[lo - 1]}"
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
      landed=$(head -n "$lo" <<<"$landed")
      k=$lo
    fi
  fi

  # Main moved during the gate (a contributor's PR, a Dependabot merge, the release): the
  # gated tree is not the one a merge would land. Compose the train again on the new main.
  g fetch -q origin main || die "cannot fetch origin after the gate of $branch: the queue is untouched"
  if [ "$(g rev-parse origin/main)" != "$base" ]; then
    log "main moved during the gate of $branch (${base:0:12}..$(g rev-parse --short=12 origin/main)): composing the train again"
    g push -q origin --delete "$branch" >/dev/null 2>&1 || true
    continue
  fi

  # One PR for the train, rebase-merged: each change stays its own commit on main.
  body=$state/body-$id.md
  {
    echo "Size exception: a train of $k change(s), each under the cap when it was queued (make ready)."
    echo
    echo "Landed by scripts/land.sh after one gate of the whole train (the \`signoff\` status of the head)."
    while read -r s sha; do
      echo
      echo "### \`$s\`: $(title "$sha" | sed 's/^/`/; s/$/`/')"
      echo
      echo "- review: $(r=$(trailer Review "$sha"); echo "${r:-not recorded}")"
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
