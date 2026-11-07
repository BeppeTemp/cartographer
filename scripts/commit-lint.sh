#!/usr/bin/env bash
# commit-lint.sh — a change's commit message follows the rules pr-title.yml applies
# to a PR title and body, so a change that lands as its own commit (a train, D1706)
# reaches the changelog as cleanly as a squashed PR.
#
#   commit-lint.sh <message file>
#
# The title is a conventional commit whose scope, when present, is one of
# `pr-triage.sh --title-scopes`; the body's conventional lines are one unwrapped line
# (`internal/changelog/gen -check-body`). Trailers of the lander (`Lander-*`, `Lander-Slug` included,
# `Review:`) are ignored. Exit 1 with the reason on stderr.
set -euo pipefail

msg=${1:?usage: commit-lint.sh <message file>}
here=$(cd "$(dirname "$0")" && pwd)
top=$(cd "$here/.." && pwd)

title=$(head -n 1 "$msg")
types='feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert'
if ! [[ "$title" =~ ^($types)(\(([a-z,-]+)\))?!?:\ [^[:space:]] ]]; then
  echo "commit-lint: the title is not a conventional commit (<type>(<scope>): <subject>): $title" >&2
  exit 1
fi
if [ -n "${BASH_REMATCH[3]}" ]; then
  allowed=$("$here/pr-triage.sh" --title-scopes)
  IFS=',' read -ra scopes <<<"${BASH_REMATCH[3]}"
  for sc in "${scopes[@]}"; do
    grep -qx "$sc" <<<"$allowed" || { echo "commit-lint: scope '$sc' is not one of: $(tr '\n' ' ' <<<"$allowed")" >&2; exit 1; }
  done
fi
body=$(tail -n +2 "$msg" | grep -vE '^(Lander-[A-Za-z]+|Review): ' || true)
if ! printf '%s' "$body" | (cd "$top" && go run ./internal/changelog/gen -check-body) >&2; then
  echo "commit-lint: a conventional line of the body is wrapped (keep each on one line)" >&2
  exit 1
fi
