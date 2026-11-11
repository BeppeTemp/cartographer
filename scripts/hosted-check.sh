#!/usr/bin/env bash
# hosted-check.sh — `make check-run` on a GitHub-hosted runner (CHECK_MAKE=./scripts/hosted-check.sh).
#
#   hosted-check.sh check-run CHECK_PKGS="<list>" [CHECK_E2E=1]
#
# What `make check` hands to CHECK_MAKE. The worktree state (uncommitted changes of tracked files
# and staged new files; never untracked ones) becomes a temporary commit on top of HEAD, built with plumbing
# in a private index so the user's index, HEAD and reflog are untouched; it is pushed to
# `check/<slug>-<path hash>-<sha12>`, hosted-check.yml is dispatched on it, this script waits and prints the
# failing log excerpt, and the branch is deleted on every exit path. A check/* branch is never a
# PR and never lands. Pushing needs write access: a contributor's push is refused (exit 75) and
# the check runs locally.
#
# Exit: 0 green, 1 red or cancelled or timed out, 2 bad call, 75 GitHub cannot run it (gh missing
# or unauthenticated, push or dispatch refused, run never appeared, network lost). Never 75 for a
# red check (D1704). The dispatch needs hosted-check.yml on the default branch: before the first
# landing it fails with 75.
#
#   HOSTED_CHECK_DRY=1            build the commit and print the branch, push nothing
#   HOSTED_CHECK_POLL=5           seconds between polls
#   HOSTED_CHECK_DISPATCH_TRIES=30  polls for the dispatched run to appear
#   HOSTED_CHECK_TRIES=360        polls of the run (30 min), then cancel and exit 1
#   HOSTED_CHECK_TAIL=150         lines of the failed log printed
set -euo pipefail

usage() { echo "usage: hosted-check.sh check-run [CHECK_PKGS=<packages>] [CHECK_E2E=1]" >&2; exit 2; }
note() { echo "hosted-check: $*" >&2; }
unavailable() { note "$*"; exit 75; }

# 1. Arguments, validated before any git or gh call (builtins only).
[ "${1:-}" = check-run ] || usage
shift
pkgs=
e2e=false
for a in "$@"; do
  case "$a" in
    CHECK_PKGS=*) pkgs=${a#CHECK_PKGS=} ;;
    CHECK_E2E=1) e2e=true ;;
    *) usage ;;
  esac
done
# Whole-string match (a newline is not in the class), and no word may start with `-`: the list
# is expanded unquoted by make, where a newline starts a command and `-exec=...` a go test flag.
[[ "$pkgs" =~ ^[A-Za-z0-9_./\ -]*$ ]] || { note "bad package list"; exit 2; }
for w in $pkgs; do
  if [[ "$w" == -* ]]; then note "bad package list: $w looks like a flag"; exit 2; fi
done

dry=${HOSTED_CHECK_DRY:-}
poll=${HOSTED_CHECK_POLL:-5}
here=${BASH_SOURCE[0]%/*}

# 2. Preconditions (75: GitHub cannot run it).
if [ -z "$dry" ]; then
  command -v gh >/dev/null 2>&1 || unavailable "gh is not installed"
  gh auth status >/dev/null 2>&1 || unavailable "gh is not authenticated"
  repo=$("$here/upstream-repo.sh" 2>/dev/null) && [ -n "$repo" ] || unavailable "cannot tell the upstream repository"
fi
cd "$(git rev-parse --show-toplevel)"
remote=origin
git remote get-url upstream >/dev/null 2>&1 && remote=upstream

# 3. The commit of the worktree state.
slug=$(git symbolic-ref --short -q HEAD || echo detached)
slug=$(printf %s "$slug" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9\n' '-' | cut -c1-40)
# Two worktrees on the same branch name (or both detached) must not sweep each other's branch.
slug=$slug-$(printf '%06x' $(($(pwd -P | cksum | cut -d' ' -f1) % 16777216)))
tmp=$(mktemp)
pushed=0 id='' done_=0 branch=''
repo=${repo:-}
# shellcheck disable=SC2329 # runs through the EXIT trap
cleanup() {
  rm -f "$tmp"
  if [ -n "$id" ] && [ "$done_" = 0 ]; then gh run cancel "$id" -R "$repo" >/dev/null 2>&1 || true; fi
  if [ "$pushed" = 1 ]; then git push -q "$remote" --delete "$branch" >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

# Tracked changes only: the private index starts as a copy of the real one (staged new files
# count) and `add -u` takes the edits and deletions of tracked files. An untracked file may be a
# stray secret and this branch is public, so none is ever pushed; the ones left out are listed.
cp "$(git rev-parse --git-path index)" "$tmp" 2>/dev/null || GIT_INDEX_FILE=$tmp git read-tree HEAD
GIT_INDEX_FILE=$tmp git add -u
untracked=$(git ls-files --others --exclude-standard)
if [ -n "$untracked" ]; then
  note "untracked files are NOT in the checked state (git add them to include them):"
  printf '  %s\n' "$untracked" | head -n 20 >&2
  # affected-pkgs.sh picks packages from the working tree, untracked files included: a new
  # package that is not pushed would be skipped and the check would come back green untested.
  if printf '%s\n' "$untracked" | grep -qE '^(cmd|internal)/'; then
    note "untracked files under cmd/ or internal/: git add the new files first (a green check would not cover them)"
    exit 2
  fi
fi
tree=$(GIT_INDEX_FILE=$tmp git write-tree)
if [ "$tree" = "$(git rev-parse 'HEAD^{tree}')" ]; then
  sha=$(git rev-parse HEAD)
else
  sha=$(GIT_AUTHOR_NAME=check GIT_AUTHOR_EMAIL=check@example.com GIT_COMMITTER_NAME=check GIT_COMMITTER_EMAIL=check@example.com \
    git -c commit.gpgsign=false commit-tree "$tree" -p HEAD -m "check: $slug worktree state")
fi
branch=check/$slug-${sha:0:12}

if [ -n "$dry" ]; then
  note "dry run: would push $sha to $remote $branch and dispatch hosted-check.yml (pkgs='$pkgs' e2e=$e2e)"
  exit 0
fi

# 4. Sweep the previous states of this worktree, left by a killed run (best effort).
while read -r _ ref; do
  b=${ref#refs/heads/}
  [[ "$b" =~ ^check/$slug-[0-9a-f]{12}$ ]] && [ "$b" != "$branch" ] || continue
  git push -q "$remote" --delete "$b" >/dev/null 2>&1 || true
done < <(git ls-remote --heads "$remote" "refs/heads/check/$slug-*" 2>/dev/null || true)

# 5. Push.
pushed=1 # set before the push: a push that fails half-way is deleted too
if ! out=$(git push -q --force "$remote" "$sha:refs/heads/$branch" 2>&1); then
  pushed=0
  unavailable "the push of $branch failed (write access needed): $out"
fi

# 6. Dispatch. Run ids only grow: the run to follow is the first one newer than the newest
# that already exists on this branch (an earlier check of the same state left a finished run).
list_runs() {
  gh run list -R "$repo" --workflow hosted-check.yml --branch "$branch" --event workflow_dispatch --commit "$sha" \
    --json databaseId --jq '.[].databaseId'
}
# Capture first: a failed list must be 75, not "no run yet" (which would reopen the stale-run case).
existing=$(list_runs 2>/dev/null) || unavailable "cannot list the runs of $branch"
before=$(printf '%s\n' "$existing" | sort -n | tail -n 1)
before=${before:-0}
gh workflow run hosted-check.yml -R "$repo" --ref "$branch" -f pkgs="$pkgs" -f e2e="$e2e" >/dev/null ||
  unavailable "cannot dispatch hosted-check.yml on $branch (is it on the default branch?)"

# 7. Find the run.
for _ in $(seq 1 "${HOSTED_CHECK_DISPATCH_TRIES:-30}"); do
  id=$(list_runs 2>/dev/null | sort -n | awk -v b="$before" '$1 > b { print; exit }' || true)
  [ -z "$id" ] || break
  sleep "$poll"
done
[ -n "$id" ] || unavailable "the dispatched run of $branch did not appear"
note "run $id on $branch"

# 8. Wait.
last='' fails=0 concl=''
for _ in $(seq 1 "${HOSTED_CHECK_TRIES:-360}"); do
  if v=$(gh run view "$id" -R "$repo" --json status,conclusion --jq '.status + " " + .conclusion' 2>/dev/null); then
    fails=0
    status=${v%% *}
    if [ "$status" != "$last" ]; then
      note "$status ($((SECONDS / 60))m$(printf %02d $((SECONDS % 60)))s)"
      last=$status
    fi
    if [ "$status" = completed ]; then done_=1; concl=${v#* }; break; fi
  else
    fails=$((fails + 1))
    [ "$fails" -lt 3 ] || unavailable "lost GitHub while waiting for run $id (cancelled if reachable, else it expires)"
  fi
  sleep "$poll"
done
if [ "$done_" = 0 ]; then
  note "run $id still running after the ceiling: cancelling"
  exit 1
fi

# 9. Verdict.
case "$concl" in
  success) echo "check: OK (hosted-check run $id)"; exit 0 ;;
  cancelled) note "run $id was cancelled (a newer check of the same branch supersedes it, or it was cancelled by hand)"; exit 1 ;;
  *)
    gh run view "$id" -R "$repo" --log-failed 2>&1 | tail -n "${HOSTED_CHECK_TAIL:-150}" >&2 || true
    note "run $id: $concl"
    exit 1
    ;;
esac
