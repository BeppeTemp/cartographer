#!/usr/bin/env bash
# worktree-clean.sh — removes what a merged PR leaves behind.
#
# For every branch checked out in a worktree under .worktrees/ it asks the
# upstream repository whether a PR with that head branch was MERGED and, if so,
# removes the worktree, the local branch and the branch on the contributor's
# fork. A squash merge leaves the branch "unmerged" for git, so the PR is the
# only proof that its work landed; it is checked, never assumed.
#
#   worktree-clean.sh             clean
#   worktree-clean.sh --dry-run   say what would be removed, remove nothing
#
# A worktree is skipped, with the reason, when: it has uncommitted changes; it
# is the current directory; its branch has no merged PR from one of this
# clone's remotes; or the branch holds a commit the merged PR does not have.
# The branch on a remote is deleted only on a remote other than the upstream
# (that is GitHub's own "delete head branch"), and only while it is still at the
# commit that was merged. A failure to delete it is reported and exits 1: the
# local cleanup is done anyway.
#
# A maintainer's change lands as a commit of a train PR (D1706), never as a PR of its
# branch: a worktree on feat/<slug> or exp/<slug> is also removed when the upstream main
# holds a commit with both `Lander-Slug: <slug>` and `Lander-Tip: <the branch head>` trailers
# (the lander keeps them), ready/<slug> is gone from the upstream (an ls-remote failure
# counts as still there), and the worktree is clean. Worktrees landed before the trailer are removed by
# hand. The worktree a running lander was started from is never removed (lander-guard.sh).
set -euo pipefail

dry=0
case "${1:-}" in
  "") ;;
  --dry-run) dry=1 ;;
  *) echo "usage: worktree-clean.sh [--dry-run]" >&2; exit 2 ;;
esac

slug_of() { git remote get-url "$1" | sed -E 's#^.*[:/]([^/:]+/[^/]+)$#\1#; s#\.git$##'; }
lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
upstream=$("$here/upstream-repo.sh")
main_root=$(git worktree list --porcelain | sed -n '1s/^worktree //p')
cwd=$(pwd -P)

# owner (lowercase) -> remote name, for every remote that is not the upstream
owners=""
forks=()
for r in $(git remote); do
  s=$(slug_of "$r")
  owners="$owners $(lower "${s%%/*}")"
  if [ "$(lower "$s")" != "$(lower "$upstream")" ]; then
    forks+=("$r")
  fi
done

# The upstream remote, and its main (fetched once) for the Lander-Slug trailers.
up_remote=""
for r in $(git remote); do
  [ "$(lower "$(slug_of "$r")")" = "$(lower "$upstream")" ] && { up_remote=$r; break; }
done
landed_main=""
if [ -n "$up_remote" ] && git fetch -q "$up_remote" main 2>/dev/null; then landed_main=$(git rev-parse FETCH_HEAD); fi

# The trailers of the upstream main, read once: "<Lander-Slug>\t<Lander-Tip>" per commit.
# (A `git log | grep -q` pipe fails with SIGPIPE on a long history under pipefail.)
landed_pairs=""
if [ -n "$landed_main" ]; then
  landed_pairs=$(git log --format='%(trailers:key=Lander-Slug,valueonly,separator=%x2C)%x09%(trailers:key=Lander-Tip,valueonly,separator=%x2C)' "$landed_main") || landed_pairs=""
fi

# landed_slug <slug> <head>: a commit of the upstream main carries both `Lander-Slug: <slug>`
# and `Lander-Tip: <head>` (the slug alone proves nothing: it can be reused, or copied into
# a PR body), and the change is no longer queued there. Fails closed: a failing ls-remote
# reads as "still queued".
landed_slug() {
  local queued
  grep -qxF "$1"$'\t'"$2" <<<"$landed_pairs" || return 1
  queued=$(git ls-remote --heads "$up_remote" "refs/heads/ready/$1" 2>/dev/null) || return 1
  [ -z "$queued" ]
}

removed=0 skipped=0 failed=0
skip() { echo "skip   $1: $2"; skipped=$((skipped + 1)); }

clean_one() {
  local path=$1 branch=$2 why slug tip pr oid owner state remote remote_tip
  if [ -n "$(git -C "$path" status --porcelain)" ]; then skip "$branch" "uncommitted changes in $path"; return; fi
  case "$cwd/" in "$path"/*) skip "$branch" "you are inside $path"; return ;; esac

  if ! why=$("$here/lander-guard.sh" "$path" 2>&1); then skip "$branch" "${why#lander-guard: }"; return; fi

  case "$branch" in feat/*|exp/*) slug=${branch#*/} ;; *) slug="" ;; esac
  if [ -n "$slug" ] && landed_slug "$slug" "$(git -C "$path" rev-parse HEAD)"; then
    if [ "$dry" = 1 ]; then
      echo "would  remove $branch (landed in a train as $slug): worktree $path and local branch"
      removed=$((removed + 1)); return
    fi
    if ! git worktree remove "$path" || ! git branch -D "$branch" >/dev/null; then
      echo "fail   $branch: git could not remove its worktree or local branch" >&2
      failed=$((failed + 1)); return
    fi
    echo "removed $branch (landed in a train as $slug): worktree and local branch"
    removed=$((removed + 1)); return
  fi

  if ! pr=$(gh pr list -R "$upstream" --head "$branch" --state merged --limit 20 \
    --json number,headRefOid,headRepositoryOwner \
    --jq '.[] | [.number, .headRefOid, (.headRepositoryOwner.login // "")] | @tsv'); then
    echo "fail   $branch: gh could not list the PRs of $upstream" >&2
    failed=$((failed + 1)); return
  fi
  tip=$(git -C "$path" rev-parse HEAD)
  oid="" owner="" state=""
  while IFS=$'\t' read -r n o l; do
    [ -n "$n" ] || continue
    case " $owners " in *" $(lower "$l") "*) ;; *) continue ;; esac
    # The merged head must contain everything the local branch holds.
    if [ "$tip" = "$o" ] || { git cat-file -e "$o^{commit}" 2>/dev/null && git merge-base --is-ancestor "$tip" "$o"; }; then
      state="#$n" oid=$o owner=$l; break
    fi
    state="#$n has not the commit $tip of the local branch"
  done <<<"$pr"
  if [ -z "$oid" ]; then
    skip "$branch" "${state:-no merged PR from one of the remotes of this clone}"
    return
  fi

  if [ "$dry" = 1 ]; then
    echo "would  remove $branch ($state merged): worktree $path, local branch, fork branch if it is still at $oid"
    removed=$((removed + 1)); return
  fi
  if ! git worktree remove "$path" || ! git branch -D "$branch" >/dev/null; then
    echo "fail   $branch: git could not remove its worktree or local branch" >&2
    failed=$((failed + 1)); return
  fi
  echo "removed $branch ($state merged): worktree and local branch"
  removed=$((removed + 1))

  for remote in ${forks[@]+"${forks[@]}"}; do
    [ "$(lower "$(slug_of "$remote" | cut -d/ -f1)")" = "$(lower "$owner")" ] || continue
    if ! remote_tip=$(git ls-remote --heads "$remote" "refs/heads/$branch" 2>/dev/null | cut -f1); then
      echo "fail   $branch: cannot read $remote (no access)" >&2; failed=$((failed + 1)); continue
    fi
    if [ -z "$remote_tip" ]; then continue; fi
    if [ "$remote_tip" != "$oid" ]; then
      skip "$branch" "$remote/$branch moved after the merge (at $remote_tip)"; continue
    fi
    if git push "$remote" --force-with-lease="refs/heads/$branch:$oid" ":refs/heads/$branch" >/dev/null 2>&1; then
      # A push that reports success is not the proof: read the remote again.
      if [ -n "$(git ls-remote --heads "$remote" "refs/heads/$branch" 2>/dev/null | cut -f1)" ]; then
        echo "fail   $branch: $remote still has its branch after the deletion" >&2
        failed=$((failed + 1))
      else
        echo "removed $branch: branch on $remote (verified gone)"
      fi
    else
      echo "fail   $branch: $remote refused the deletion of its branch (token or permission)" >&2
      failed=$((failed + 1))
    fi
  done
}

path="" branch=""
while IFS= read -r line; do
  case "$line" in
    "worktree "*) path=${line#worktree }; branch="" ;;
    "branch refs/heads/"*) branch=${line#branch refs/heads/} ;;
    "")
      if [ -n "$path" ] && [ -n "$branch" ] && [ "$path" != "$main_root" ] && [ "${path#"$main_root"/.worktrees/}" != "$path" ]; then
        clean_one "$path" "$branch" </dev/null
      fi
      path="" branch="" ;;
  esac
done < <(git worktree list --porcelain; echo)

if [ "$dry" = 0 ]; then
  git worktree prune
  git fetch --all --prune --quiet 2>/dev/null || true
fi
echo "worktree-clean: $removed removed, $skipped skipped, $failed failed"
[ "$failed" = 0 ]
