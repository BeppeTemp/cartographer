#!/usr/bin/env bash
# unlanded.sh — finished work that no queue shows (make unlanded).
#
#   make unlanded
#
# Read-only. A branch ahead of main with no Lander-Slug is listed as not queued: it may have landed under
# another slug or as a contributor's PR. One line per thing a session can leave behind that neither `make issues`, `make
# status` nor `make land-status` would list, each indented two spaces:
#   branch <name>: <n> commit(s)   a local branch with commits not on origin/main whose slug (the
#                                  branch name after its prefix) has no `Lander-Slug:` trailer on
#                                  origin/main and no ready/<slug> or failed/<slug> ref on origin
#   worktree <path>                a worktree with uncommitted changes to tracked files
#   wip <slug>                     a `wip/<slug>` branch on origin: a change parked, not queued
# Exit 0 and no output when there is nothing, 1 when there is. `make status` prints the same lines
# under `unlanded:`. A change that cannot be queued when its session ends is parked: pushed to
# `wip/<slug>` with one issue labelled `needs-maintainer` that names what blocks it (CONTRIBUTING.md
# §Pull requests); `make ready` deletes the branch once it queues the change; the release candidate
# refuses while one exists (release-train). Its fetch updates remote-tracking refs, as every fetch
# does; it never pushes or writes a ref on origin. Plain bash 3.2 (the macOS default).
set -uo pipefail
export LC_ALL=C

cd "$(git rev-parse --show-toplevel)" || exit 1
git fetch -q --no-tags --prune origin main '+refs/heads/ready/*:refs/remotes/origin/ready/*' \
  '+refs/heads/failed/*:refs/remotes/origin/failed/*' '+refs/heads/wip/*:refs/remotes/origin/wip/*' >/dev/null 2>&1 || true

out=
trailer() { git log -1 --format=%B "$1" | git interpret-trailers --parse | sed -n "s/^$2: //p" | tail -n 1; }
landed() { # landed <slug>: main holds a commit whose Lander-Slug trailer is the slug, whole
  local c
  for c in $(git log origin/main --format=%H --fixed-strings --grep="Lander-Slug: $1"); do
    [ "$(trailer "$c" Lander-Slug)" = "$1" ] && return 0
  done
  return 1
}

git rev-parse -q --verify refs/remotes/origin/main >/dev/null || { echo "unlanded: origin/main is unknown" >&2; exit 1; }
while read -r b; do
  case "$b" in main | train/* | ready/* | failed/* | wip/*) continue ;; esac
  n=$(git rev-list --count "refs/remotes/origin/main..refs/heads/$b" 2>/dev/null) || continue
  [ "${n:-0}" -gt 0 ] || continue
  slug=${b#*/}
  git rev-parse -q --verify "refs/remotes/origin/ready/$slug" >/dev/null && continue
  git rev-parse -q --verify "refs/remotes/origin/failed/$slug" >/dev/null && continue
  landed "$slug" && continue
  out+="  branch $b: $n commit(s) not on origin/main, not queued"$'\n'
done < <(git for-each-ref --format='%(refname:lstrip=2)' refs/heads)

# Only the main checkout and the worktrees of .worktrees/: the lander's own (detached, under its state
# directory) are not a session's work.
common=$(git rev-parse --path-format=absolute --git-common-dir)
main_wt=$(cd "$(dirname "$common")" && pwd -P)
while read -r p; do
  [ -n "$p" ] || continue
  case "$(cd "$p" 2>/dev/null && pwd -P)/" in "$main_wt/" | "$main_wt/.worktrees/"*) ;; *) continue ;; esac
  [ -z "$(git -C "$p" status --porcelain --untracked-files=no 2>/dev/null)" ] || out+="  worktree $p: uncommitted changes"$'\n'
done < <(git worktree list --porcelain | sed -n 's/^worktree //p')

while read -r w; do
  [ -n "$w" ] || continue
  out+="  wip ${w#wip/}: parked on origin, not queued"$'\n'
done < <(git for-each-ref --format='%(refname:lstrip=3)' refs/remotes/origin/wip)

[ -z "$out" ] && exit 0
printf '%s' "$out"
exit 1
