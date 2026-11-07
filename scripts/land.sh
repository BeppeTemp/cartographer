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
# `D-new-<slug>` citation is rewritten, in the change's own commit.
#
# The train branch `train/<UTC time>` is gated by scripts/signoff.sh (the gate, the web
# legs and the Windows leg when needed, the CI fallback on exit 75, the push and the
# `signoff` status: one gate for the whole train). The signoff itself retries once the
# Go tests a red run names (its step 4c): green, the train lands on that gate and a `bug`
# issue names them; red, the failure is confirmed and bisection starts without a re-run.
# Any other red: one re-run of the same tree; green then means a flaky test, so the train
# lands and a `bug` issue names the tests. Red twice: bisection over the train's prefixes
# finds the first failing change, which is rejected, and the green prefix before it lands. Green: one PR, title
# `chore(dev): train <id>, <k> changes`, rebase-merged by auto-merge so every change stays
# its own conventional commit on main; once merged, the landed queue entries are deleted
# (a queued change whose diff main already holds is dequeued, not rejected). On macOS the
# lander holds `caffeinate -i` while it runs: an idle Mac on battery would sleep through it.
#
# A rejected change moves to `refs/heads/failed/<slug>`: a commit on top of it whose
# message is the reason, readable from any clone (`git log -1 origin/failed/<slug>`).
# One lander at a time: per machine a lock in the state dir, across machines an open PR
# whose head is `train/*` (another lander's train: wait for it).
#
#   LAND_MAX (8)  LAND_POLL (30 s)  LAND_MERGE_TRIES (90 polls)  LAND_STATE (state dir)
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
open_trains() { gh pr list -R "$repo" --state open --json number,headRefName --jq '.[] | select(.headRefName | startswith("train/")) | "\(.number) \(.headRefName)"'; }

if [ "${1:-}" = --status ]; then
  fetch || true
  echo "queue:"; queue | while read -r s sha; do
    extra=; a=$(trailer Lander-After "$sha"); [ -z "$a" ] || extra=" (after $a)"
    [ -z "$(trailer Lander-Hold "$sha")" ] || extra="$extra (held)"
    echo "  $s: $(title "$sha")$extra"
  done
  echo "open train PR:"; open_trains | sed 's/^/  #/' || true
  echo "rejected:"; g for-each-ref --format='%(refname:lstrip=4) %(contents:subject)' refs/remotes/origin/failed/ | sed 's/^/  /'
  if running; then echo "lander: running (pid $(cat "$state/pid")), log $state/log"; else echo "lander: not running"; fi
  exit 0
fi
dry=false
[ "${1:-}" != --dry-run ] || dry=true

# One lander per machine and repository.
if ! $dry; then
  if running && [ "$(cat "$state/pid")" != $$ ]; then echo "land: a lander already runs here"; exit 0; fi
  echo $$ >"$state/pid"
  # The root this lander runs from: removing that worktree deletes the scripts under it
  # (scripts/lander-guard.sh, used by worktree-rm and worktree-clean, reads this file).
  (cd "$here/.." && pwd -P) >"$state/root"
  trap 'rm -f "$state/pid" "$state/root"' EXIT
  # The lander mostly waits (on the gate, on GitHub). A Mac on battery sleeps when idle, and
  # asleep the lander advances only in its dark wakes, a quarter of an hour apart, waking
  # with the network still down: caffeinate keeps it awake (not a closed lid) until the
  # lander exits.
  if command -v caffeinate >/dev/null 2>&1; then caffeinate -i -w $$ >/dev/null 2>&1 </dev/null & fi
  perm=$(gh repo view "$repo" --json viewerPermission --jq .viewerPermission 2>/dev/null || echo UNKNOWN)
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

breaking() { # a feat, perf or refactor title, or a breaking change: held while the freeze is open
  local t
  t=$(title "$1")
  [[ "$t" =~ ^(feat|perf|refactor)(\(|:|!) ]] || [[ "$t" =~ ^[a-z]+(\([^\)]*\))?!: ]] ||
    g log -1 --format=%B "$1" | grep -q '^BREAKING CHANGE'
}

# compose: selects the train into TRAIN (lines "<slug> <sha>"), in landing order.
compose() {
  local q freeze=false pass changed s sha after
  q=$(queue)
  TRAIN=
  [ -n "$q" ] || return 0
  if GH_REPO=$repo "$here/freeze-open.sh" >/dev/null 2>&1; then freeze=true; fi
  local failed
  failed=$(g for-each-ref --format='%(refname:lstrip=4)' refs/remotes/origin/failed/)
  for pass in 1 2 3 4 5 6 7 8; do
    changed=false
    while read -r s sha; do
      [ -n "$s" ] || continue
      grep -qx "$s $sha" <<<"$TRAIN" && continue
      [ "$(printf '%s\n' "$TRAIN" | grep -c .)" -lt "$max" ] || break
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

# number_decisions: in the train worktree, after a cherry-pick, numbers the pending decisions
# the commit adds and rewrites the known placeholders (MAP: "<placeholder>=D<n>" words) in
# the files the commit touches. Text already on main is never rewritten, and test files
# are left alone (fixtures may spell a placeholder). LEFT is set to a placeholder the
# commit still cites that is neither on main nor earlier in the train. Not run in a
# subshell: MAP carries the numbers to the later changes of the train.
number_decisions() {
  local f ph n pair num touched
  touched=$(git diff-tree --no-commit-id --name-only -r HEAD | grep -v '_test\.go$' || true)
  for f in $(grep -E '^docs/decisions/D-new-[a-z0-9-]+\.md$' <<<"$touched" || true); do
    [ -f "$f" ] || continue
    ph=$(basename "$f" .md)
    n=$(ls docs/decisions | sed -nE 's/^D([0-9]+)-.*/\1/p' | sort -n | tail -n 1)
    n=${n:-0}
    [ "${FLOOR:-0}" -le "$n" ] || n=$FLOOR
    n=$((n + 1))
    git mv "$f" "docs/decisions/D$n-${ph#D-new-}.md"
    touched=$(printf '%s\n' "$touched" | sed "s#^$f\$#docs/decisions/D$n-${ph#D-new-}.md#")
    MAP="$MAP $ph=D$n"
  done
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

gate() { # $1 = log file; signoff.sh on the train branch: exit 0 green, 1 red, 2 infrastructure
  local rc
  # Streamed to the lander's log as it runs (a gate takes minutes), and kept in $1.
  set +e
  (cd "$wt" && "$here/signoff.sh") 2>&1 | tee "$1" >&2
  rc=${PIPESTATUS[0]}
  set -e
  [ "$rc" = 0 ] && return 0
  if grep -qE 'failed \(exit [0-9]+\): nothing pushed, nothing posted|of ci.yml run [0-9]+ is ' "$1"; then return 1; fi
  return 2
}

# The tests a red gate log names: Go top-level tests, vitest cases, Playwright specs.
failing() { # $1 = gate log; vitest and Playwright colour their lines
  sed "s/$(printf '\033')\[[0-9;]*m//g" "$1" | sed -n \
    -e 's/^--- FAIL: \([A-Za-z0-9_]*\).*/\1/p' \
    -e 's/^ FAIL  \([^ ]*\) > .*/\1/p' \
    -e 's/^ *✘ *[0-9]* *\([^ ]*\) .*/\1/p' | sort -u | head -n 10 | tr '\n' ' ' | sed 's/ $//' || true
}
issue_flaky() { # $1 = names, $2 = what happened, $3 = body
  gh issue create -R "$repo" --label bug --title "flaky: $1 $2" --body "$3" >/dev/null ||
    log "warning: could not open the flaky issue"
}
# A green gate whose signoff retried named tests: they are flaky, the train lands, a bug names them.
# The line is in the log among the tests' own output: only package.Test names make the title.
flaky_issue() { # $1 = gate log, $2 = what happened
  local names
  names=$(sed -n 's/^signoff: flaky: \(.*\) failed and passed on a targeted retry$/\1/p' "$1" |
    grep -E '^[A-Za-z0-9_.-]+( [A-Za-z0-9_.-]+)*$' | tr '\n' ' ' | sed 's/ $//' || true)
  [ -n "$names" ] || return 0
  log "green after the signoff's targeted retry: flaky $names"
  issue_flaky "$names" "$2" \
    "The gate of the train \`$branch\` failed on $names and passed when the signoff ran their packages once more, the rest of the gate green. A retry is not a fix: find why they fail."
}

wt=$state/train
while :; do
  fetch || die "cannot fetch origin"
  if ! $dry; then
    others=$(open_trains || true)
    if [ -n "$others" ]; then
      log "train PR #${others%% *} is open (another lander, or one to merge by hand): waiting"
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
  MAP=
  # The newest issue or PR number: pending decisions are numbered above it.
  FLOOR=$(gh api "repos/$repo/issues?state=all&sort=created&direction=desc&per_page=1" --jq '.[0].number // 0' 2>/dev/null || true)
  [[ "$FLOOR" =~ ^[0-9]+$ ]] || FLOOR=0
  landed= commits=()
  while read -r s sha; do
    if ! git cherry-pick --allow-empty "$sha" >/dev/null 2>&1; then
      git cherry-pick --abort >/dev/null 2>&1 || true
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
    tip=$(trailer Lander-Tip "$sha")
    msg=$(git log -1 --format=%B | grep -vE '^(Lander-[A-Za-z]+|Review): ' || true)
    # The lander's other trailers stay off main; the slug and the branch head ready.sh
    # squashed stay, so `make worktree-clean` can tell that the work of a feat/<slug>
    # worktree landed (the train PR is not its PR). Any copy in the message was dropped above.
    targs=(--trailer "Lander-Slug: $s")
    [ -z "$tip" ] || targs+=(--trailer "Lander-Tip: $tip")
    printf '%s\n' "$msg" | git interpret-trailers "${targs[@]}" | git commit -q --amend --allow-empty -F -
    number_decisions
    if [ -n "$LEFT" ]; then
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
    rc2=1
    if grep -q '^signoff: confirmed on a targeted retry:' "$glog"; then
      log "red, and red again on the signoff's targeted retry of${fails:+ $fails}: not a flake"
    else
      rc2=0; gate "$glog.2" || rc2=$?
      [ "$rc2" != 2 ] || die "the gate of $branch failed for a reason that is not a test (see $glog.2): the queue is untouched"
    fi
    if [ "$rc2" = 0 ]; then
      log "red once, green on the re-run: a flaky test (${fails:-not named})"
      issue_flaky "${fails:-the gate of a train}" "failed once and passed on a re-run" \
        "The gate of the train \`$branch\` failed, then passed on the same tree. Failing tests: ${fails:-none named in the log}. A re-run is not a fix: find why it fails."
    else
      lo=0 hi=$k good=
      while [ $((hi - lo)) -gt 1 ]; do
        m=$(( (lo + hi) / 2 ))
        git reset -q --hard "${commits[m - 1]}"
        r=0; gate "$glog.p$m" || r=$?
        [ "$r" != 2 ] || die "the gate of a prefix of $branch failed for a reason that is not a test: the queue is untouched"
        if [ "$r" = 0 ]; then lo=$m; good=$(git rev-parse HEAD); else hi=$m; fi
      done
      bad=$(sed -n "${hi}p" <<<"$landed")
      reject "${bad%% *}" "${bad#* }" "the train gate is red with it and green without it (tests: ${fails:-see the gate log})"
      if [ "$lo" = 0 ]; then
        g push -q origin --delete "$branch" >/dev/null 2>&1 || true
        continue
      fi
      git reset -q --hard "$good"
      g push -q --force origin "$good:refs/heads/$branch"
      landed=$(head -n "$lo" <<<"$landed")
      k=$lo
    fi
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
  # Auto-merge waits for the required checks; on a PR whose checks are already green
  # GitHub refuses to arm it, and the merge is done at once instead.
  gh pr merge "$num" -R "$repo" --rebase --auto >/dev/null 2>&1 ||
    gh pr merge "$num" -R "$repo" --rebase >/dev/null ||
    die "could not merge or arm auto-merge on #$num: merge it by hand, then run the lander again"
  st=
  for _ in $(seq 1 "${LAND_MERGE_TRIES:-90}"); do
    st=$(gh pr view "$num" -R "$repo" --json state --jq .state 2>/dev/null || true)
    [ "$st" != MERGED ] && [ "$st" != CLOSED ] || break
    sleep "$poll"
  done
  [ "$st" = MERGED ] || die "train PR #$num is ${st:-unknown} after the wait: look at its checks; the queue is untouched"
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
