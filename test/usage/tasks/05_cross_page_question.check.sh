#!/usr/bin/env bash
# The answer draws on more than one page (the session lifetime and the token rotation); no write.
. "$(dirname "$0")/../lib.sh"
grep -qiE 'eight|8 ?h' "$USAGE_RESULT" || { echo "the answer misses the session lifetime"; exit 1; }
grep -qiE 'ninety|90' "$USAGE_RESULT" || { echo "the answer misses the token rotation period"; exit 1; }
[ "$(usage_commits)" = "$USAGE_SEED_COMMITS" ] || { echo "a question changed the KB"; exit 1; }
