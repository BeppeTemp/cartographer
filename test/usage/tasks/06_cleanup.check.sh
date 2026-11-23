#!/usr/bin/env bash
# The old page is gone or no longer current, and no link is left broken.
. "$(dirname "$0")/../lib.sh"
usage_lint_clean || exit 1
if text=$(usage_call concept_read '{"id":"dev/old-auth-notes"}' 2>/dev/null); then
  grep -qE 'status: *active' <<<"$text" && { echo "dev/old-auth-notes is still marked active"; exit 1; }
fi
[ "$(usage_commits)" -gt "$USAGE_SEED_COMMITS" ] || { echo "nothing was done"; exit 1; }
grep -q 'auth-flow' <(ls "$USAGE_KB/data/dev") || { echo "the page that supersedes it is gone"; exit 1; }
