#!/usr/bin/env bash
# upstream-repo.sh — prints owner/name of the upstream repository (D955).
#
# The `upstream` remote when present; otherwise `origin`, but when origin is a
# fork (gh says so) its parent. A contributor who cloned their own fork as
# `origin` is ADMIN there: the role must be read on the repository the PR targets.
#
#   upstream-repo.sh                  print it
#   upstream-repo.sh --require-remote also fail (exit 1) when there is no `upstream`
#                                     remote and origin is a fork: a worktree must
#                                     never branch from a stale fork main
set -euo pipefail

slug_of() { git remote get-url "$1" | sed -E 's#^.*[:/]([^/:]+/[^/]+)$#\1#; s#\.git$##'; }

if git remote get-url upstream >/dev/null 2>&1; then
  slug_of upstream
  exit 0
fi
origin=$(slug_of origin)
# REST, not `gh repo view`: that one is GraphQL, which some environments (cloud
# agent sessions behind a proxy) refuse while REST works.
if ! parent=$(gh api "repos/$origin" \
  --jq 'if .fork then .parent.full_name else "" end' 2>/dev/null); then
  if [ "${1:-}" = "--require-remote" ]; then
    echo "the fork check of $origin could not run (gh failed): authenticate gh or add the upstream remote" >&2
    exit 1
  fi
  echo "warning: the fork check of $origin could not run (gh failed); assuming it is the upstream" >&2
  parent=""
fi
if [ -n "$parent" ]; then
  if [ "${1:-}" = "--require-remote" ]; then
    echo "origin ($origin) is a fork of $parent and there is no upstream remote: git remote add upstream https://github.com/$parent.git" >&2
    exit 1
  fi
  echo "$parent"
else
  echo "$origin"
fi
