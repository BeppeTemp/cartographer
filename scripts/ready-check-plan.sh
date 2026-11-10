#!/usr/bin/env bash
# ready-check-plan.sh — what the hosted pre-check of a queued change runs (ready-check.yml,
# D1877). Reads the paths the change touches on stdin, one per line, and
# prints `name=value` lines, ready for `>> "$GITHUB_OUTPUT"`:
#
#   pkgs    the packages a code change can break on Windows (scripts/affected-pkgs.sh
#           --code, space-separated; empty: no Windows leg)
#   count   how many shards they are split into (scripts/test-shard.sh, D785): 4 when the
#           slow packages are in (internal/mcpserver, cmd/cartographer, or everything),
#           2 past four packages, else 1
#   shards  the shard indexes as a JSON array for the job matrix; never empty, as a
#           skipped job's matrix is still parsed
#   web     true when the change touches the frontend or its bundle (web/, internal/webui/):
#           its unit tests and bundle checks run, the browser suite stays in the train gate
#
# The legs are the ones the build host's `make check` does not run and that turned trains
# red: a change that breaks them is gated alone instead (scripts/land.sh).
# internal/repodocs/ready_check_test.go pins the table. Plain bash 3.2.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
paths=$(cat)
pkgs=$(printf '%s\n' "$paths" | "$here/affected-pkgs.sh" --code | tr '\n' ' ')
pkgs=${pkgs% }

count=1
if [ -z "$pkgs" ]; then
  count=1
elif printf '%s\n' $pkgs | grep -qxE '\./\.\.\.|\./internal/mcpserver|\./cmd/cartographer'; then
  count=4
elif [ "$(printf '%s\n' $pkgs | grep -c .)" -gt 4 ]; then
  count=2
fi
shards=$(seq 0 $((count - 1)) | paste -sd, -)

web=false
if printf '%s\n' "$paths" | grep -qE '^(web|internal/webui)/'; then web=true; fi

echo "pkgs=$pkgs"
echo "count=$count"
echo "shards=[$shards]"
echo "web=$web"
