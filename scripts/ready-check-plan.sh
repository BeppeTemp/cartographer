#!/usr/bin/env bash
# ready-check-plan.sh — what the hosted pre-check of a queued change runs (ready-check.yml,
# D1877). Reads the paths the change's queued chain touches on stdin, one per line
# (scripts/ready-check-base.sh prints them), and
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
#           its unit tests, its bundle checks and the browser suite run (`make e2e-web`
#           without the @video test, the `web (browser)` leg of the train gate, #2437).
#           scripts/hosted-check.sh asks for its browser leg by the same pattern
#   audit   true when the chain selects internal/provisioning (scripts/affected-pkgs.sh: a
#           non-test Go file under cmd/ or internal/, or the package itself): the `readers` job then runs
#           TestSourceSitesAreClassified, a reader the Windows leg (--code) leaves out (#2437)
#
# `ready-check-plan.sh --count <packages...>` prints only the count for those packages: the
# hosted check (scripts/hosted-check.sh) splits its Windows leg by the same rule.
#
# The legs are the ones the build host's `make check` does not run and that turned trains
# red: the lander reads the verdict on a unit's tip and gates a red unit alone instead
# (scripts/land.sh).
# internal/repodocs/landertest/ready_check_test.go pins the table. Plain bash 3.2.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)

count_of() { # $* = the packages
  if [ "$#" = 0 ]; then
    echo 1
  elif printf '%s\n' "$@" | grep -qxE '\./\.\.\.|\./internal/mcpserver|\./cmd/cartographer'; then
    echo 4
  elif [ "$#" -gt 4 ]; then
    echo 2
  else
    echo 1
  fi
}
if [ "${1:-}" = --count ]; then shift; count_of "$@"; exit 0; fi

paths=$(cat)
pkgs=$(printf '%s\n' "$paths" | "$here/affected-pkgs.sh" --code | tr '\n' ' ')
pkgs=${pkgs% }

# shellcheck disable=SC2086  # the packages are a word list on purpose
count=$(count_of $pkgs)
shards=$(seq 0 $((count - 1)) | paste -sd, -)

web=false
if printf '%s\n' "$paths" | grep -qE '^(web|internal/webui)/'; then web=true; fi

# One rule with the fast tier: the seeds of affected-pkgs.sh, before --code drops the readers.
audit=false
seeds=$(printf '%s\n' "$paths" | "$here/affected-pkgs.sh" --seeds)
if printf '%s\n' "$seeds" | grep -qxF ./internal/provisioning; then audit=true; fi

echo "pkgs=$pkgs"
echo "count=$count"
echo "shards=[$shards]"
echo "web=$web"
echo "audit=$audit"
