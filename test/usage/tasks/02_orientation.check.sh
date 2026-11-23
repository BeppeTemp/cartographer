#!/usr/bin/env bash
# The answer comes from the pages, and nothing was written.
. "$(dirname "$0")/../lib.sh"
grep -qiE 'eight|8 ?h' "$USAGE_RESULT" || { echo "the answer does not give the session lifetime (eight hours)"; exit 1; }
[ "$(usage_commits)" = "$USAGE_SEED_COMMITS" ] || { echo "a question changed the KB"; exit 1; }
usage_lint_clean
