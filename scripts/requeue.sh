#!/usr/bin/env bash
# requeue.sh — a maintainer's queued or rejected change is rebuilt on the current main.
#
#   make requeue SLUG=<slug> [FIRST=1]
#
# The source is `ready/<slug>`, else the commit queued under `failed/<slug>` (`failed/<slug>^`).
# The slug and every queued change that follows it (its `Lander-After` chain, as the lander
# reads it) are rebuilt in order in a scratch worktree under the lander's state directory,
# never in the caller's tree: the first onto origin/main (onto its still-queued predecessor when
# it has one), each next one onto the rebuilt one before it. Message and trailers are kept
# verbatim, so is the author date, so is the committer date (the queue position); the rebuilt
# commits are new objects, so each gets a fresh pre-check (the tip's too: it replays the chain).
# FIRST=1 dates them before the oldest queue entry instead, in chain order, and adds
# `Lander-Now: yes` to the first. Every ref is replaced in ONE atomic push, each with a lease on
# the sha it was read at; the `failed/<slug>` it started from is deleted in the same push. Then
# the lander is started as `make ready` does (READY_NO_LANDER=1: not). One line per ref names the
# old and the new sha.
#
# Nothing is pushed on an error: an unknown slug lists the queue, a change that does not apply
# names its slug and files, one that became empty is already on main, a lease that fails means
# the queue changed. A lander gating the old commits is safe: its dequeue leaves a replaced ref
# queued and the next loop dequeues it by its slug and tip trailers.
# Plain bash 3.2 (the macOS default).
set -euo pipefail

die() { echo "requeue: $*" >&2; exit 1; }
here=$(cd "$(dirname "$0")" && pwd)
cd "$(git rev-parse --show-toplevel)"

slug=${SLUG:-}
[[ "$slug" =~ ^[a-z0-9][a-z0-9-]*$ ]] || die "SLUG=<slug> is required (lowercase letters, digits and dashes)"
case "${FIRST:-}" in "" | 1) ;; *) die "FIRST is 1 or not set" ;; esac

"$here/upstream-repo.sh" --require-remote >/dev/null || die "cannot tell the upstream repository"
repo=$("$here/upstream-repo.sh")
perm=$(gh repo view "$repo" --json viewerPermission --jq .viewerPermission 2>/dev/null || echo UNKNOWN)
case "$perm" in
  ADMIN | MAINTAIN | WRITE) ;;
  *) die "permission on $repo is $perm: only a maintainer requeues" ;;
esac
git remote get-url upstream >/dev/null 2>&1 && die "the lander works on the upstream repository itself: origin must be it, not a fork"

common=$(git rev-parse --path-format=absolute --git-common-dir)
main_repo=${common%/.git}
g() { git -C "$main_repo" "$@"; }
key=$(printf %s "$main_repo" | cksum | cut -d' ' -f1)
state=${LAND_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/cartographer-lander/$key}
mkdir -p "$state"

g fetch -q --no-tags --prune origin main '+refs/heads/ready/*:refs/remotes/origin/ready/*' '+refs/heads/failed/*:refs/remotes/origin/failed/*' ||
  die "cannot fetch origin"
main_ref=refs/remotes/origin/main

# As scripts/land.sh reads a trailer: the first one, the space after the colon optional.
trailer() { g log -1 --format=%B "$2" | git interpret-trailers --parse | sed -n "s/^$1: *//p" | head -n 1; }
queue=$(g for-each-ref --sort=committerdate --format='%(refname:lstrip=4) %(objectname)' refs/remotes/origin/ready/)
failed=$(g for-each-ref --format='%(refname:lstrip=4)' refs/remotes/origin/failed/)
listing() { echo "queued: $(cut -d' ' -f1 <<<"$queue" | tr '\n' ' ')" >&2; echo "rejected: $(tr '\n' ' ' <<<"$failed")" >&2; }

# The queued sha of a slug: its ready/ ref, else the commit failed/<slug> was made on.
source_of() {
  g rev-parse -q --verify "refs/remotes/origin/ready/$1^{commit}" 2>/dev/null ||
    g rev-parse -q --verify "refs/remotes/origin/failed/$1^" 2>/dev/null
}
sha0=$(source_of "$slug") || { echo "requeue: $slug is neither queued nor rejected" >&2; listing; exit 1; }
from0=ready
g rev-parse -q --verify "refs/remotes/origin/ready/$slug^{commit}" >/dev/null 2>&1 || from0=failed
# The leases of the push are the shas read here, with the sources of the rebuilt commits (a
# rejected first change has no ready/ ref: its lease is "must not exist"), never a later read.
fsha0=
[ "$from0" != failed ] || fsha0=$(g rev-parse "refs/remotes/origin/failed/$slug")

# The chain: the slug, then every queued change whose Lander-After is in it, discovery order
# (a predecessor always comes before its follower).
members=("$slug") shas=("$sha0")
changed=true
while $changed; do
  changed=false
  while read -r s sha; do
    [ -n "$s" ] || continue
    skip=false
    for m in "${members[@]}"; do [ "$m" != "$s" ] || skip=true; done
    ! $skip || continue
    a=$(trailer Lander-After "$sha")
    [ -n "$a" ] || continue
    for m in "${members[@]}"; do
      if [ "$m" = "$a" ]; then members+=("$s") shas+=("$sha"); changed=true; break; fi
    done
  done <<<"$queue"
done
n=${#members[@]}

# The new parent of the first member: its still-queued (or rejected) predecessor, else main.
first_parent=$(g rev-parse "$main_ref^{commit}")
a0=$(trailer Lander-After "$sha0")
if [ -n "$a0" ] && [ "$a0" != "$slug" ]; then
  if p=$(g rev-parse -q --verify "refs/remotes/origin/ready/$a0^{commit}" 2>/dev/null) ||
    p=$(g rev-parse -q --verify "refs/remotes/origin/failed/$a0^" 2>/dev/null); then first_parent=$p; fi
fi

oldest=$(g for-each-ref --sort=committerdate --count=1 --format='%(committerdate:unix)' refs/remotes/origin/ready/)
[ -n "$oldest" ] || oldest=$(date +%s)

wt=$state/requeue.$$
cleanup() { g worktree remove --force "$wt" >/dev/null 2>&1 || rm -rf "$wt"; g worktree prune >/dev/null 2>&1 || true; }
trap cleanup EXIT
g worktree prune >/dev/null 2>&1 || true
g worktree add -q --detach "$wt" "$first_parent" >/dev/null 2>&1 || die "cannot create the scratch worktree $wt"

news=()
i=0
while [ "$i" -lt "$n" ]; do
  s=${members[i]} old=${shas[i]}
  parent=$first_parent
  if [ "$i" -gt 0 ]; then
    a=$(trailer Lander-After "$old")
    j=0
    while [ "$j" -lt "$i" ]; do
      [ "${members[j]}" != "$a" ] || parent=${news[j]}
      j=$((j + 1))
    done
  fi
  git -C "$wt" checkout -q --detach "$parent" || die "cannot check out the parent of $s"
  if ! git -C "$wt" cherry-pick -n "$old" >/dev/null 2>&1; then
    files=$(git -C "$wt" diff --name-only --diff-filter=U | tr '\n' ' ')
    git -C "$wt" cherry-pick --abort >/dev/null 2>&1 || git -C "$wt" reset -q --hard >/dev/null 2>&1 || true
    die "$s does not apply on the current main (conflicts: ${files:-none listed}): fix it on its branch and make ready again; nothing was pushed"
  fi
  tree=$(git -C "$wt" write-tree)
  [ "$tree" != "$(g rev-parse "$parent^{tree}")" ] || die "$s is already on main: delete ready/$s (git push origin :refs/heads/ready/$s); nothing was pushed"
  msg=$(mktemp "${TMPDIR:-/tmp}/requeue-msg.XXXXXX")
  g cat-file commit "$old" | sed '1,/^$/d' >"$msg"
  cdate=$(g log -1 --format=%cd --date=raw "$old")
  if [ -n "${FIRST:-}" ]; then
    cdate="$((oldest - n + i)) +0000"
    if [ "$i" = 0 ]; then git interpret-trailers --in-place --if-exists doNothing --trailer "Lander-Now: yes" "$msg"; fi
  fi
  new=$(GIT_AUTHOR_NAME=$(g log -1 --format=%an "$old") GIT_AUTHOR_EMAIL=$(g log -1 --format=%ae "$old") GIT_AUTHOR_DATE=$(g log -1 --format=%ad --date=raw "$old") \
    GIT_COMMITTER_NAME=$(g log -1 --format=%cn "$old") GIT_COMMITTER_EMAIL=$(g log -1 --format=%ce "$old") GIT_COMMITTER_DATE=$cdate \
    g commit-tree "$tree" -p "$parent" -F "$msg") || die "cannot create the commit of $s"
  rm -f "$msg"
  news+=("$new")
  git -C "$wt" reset -q --hard >/dev/null 2>&1 || true
  i=$((i + 1))
done

# One atomic push: every ref with a lease on what it was read at; the rejection ref this started
# from goes in the same push.
args=(--atomic)
i=0
while [ "$i" -lt "$n" ]; do
  s=${members[i]}
  cur=${shas[i]}
  [ "$i" -gt 0 ] || [ "$from0" != failed ] || cur=
  args+=("--force-with-lease=refs/heads/ready/$s:$cur")
  i=$((i + 1))
done
if [ "$from0" = failed ]; then
  args+=("--force-with-lease=refs/heads/failed/$slug:$fsha0")
fi
refspecs=()
i=0
while [ "$i" -lt "$n" ]; do refspecs+=("${news[i]}:refs/heads/ready/${members[i]}"); i=$((i + 1)); done
[ "$from0" != failed ] || refspecs+=(":refs/heads/failed/$slug")
if ! out=$(g push -q "${args[@]}" origin "${refspecs[@]}" 2>&1); then
  # A lease that fails says `stale info` (the remote's advertisement already differs) or, when the
  # ref moved after it, `cannot lock ref ... but expected`: the queue moved since it was read. Any other failure
  # (the network, a refusal of the remote) is shown as it is.
  if grep -qE 'stale info|cannot lock ref' <<<"$out"; then die "the queue changed: run it again; nothing was pushed"; fi
  die "the push failed, nothing was pushed: $out"
fi

i=0
while [ "$i" -lt "$n" ]; do
  echo "requeue: ready/${members[i]} ${shas[i]:0:12} -> ${news[i]:0:12}"
  i=$((i + 1))
done
[ -n "${READY_NO_LANDER:-}" ] || "$here/land.sh" --daemon
