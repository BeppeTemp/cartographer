#!/usr/bin/env bash
# lander-guard.sh — refuses (exit 1, reason on stderr) to remove the worktree a running
# lander was started from: its scripts live there, and the lander's next gate would fail
# with "No such file or directory".
#
#   lander-guard.sh <worktree path>
#
# land.sh writes the root it runs from to $state/root beside $state/pid; the state
# directory is computed here as land.sh computes it.
set -euo pipefail

path=$(cd "${1:?usage: lander-guard.sh <worktree path>}" 2>/dev/null && pwd -P) || exit 0
common=$(git rev-parse --path-format=absolute --git-common-dir)
main_repo=${common%/.git}
key=$(printf %s "$main_repo" | cksum | cut -d' ' -f1)
state=${LAND_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/cartographer-lander/$key}

[ -f "$state/pid" ] && [ -f "$state/root" ] && kill -0 "$(cat "$state/pid")" 2>/dev/null || exit 0
if [ "$(cat "$state/root")" = "$path" ]; then
  echo "lander-guard: the lander (pid $(cat "$state/pid")) runs from $path: removing it breaks its next gate; wait for it to exit (make land-status)" >&2
  exit 1
fi
