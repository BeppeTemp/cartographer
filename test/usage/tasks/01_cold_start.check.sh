#!/usr/bin/env bash
# The agent only looked: the KB is as it was and its lint is clean.
. "$(dirname "$0")/../lib.sh"
usage_lint_clean || exit 1
[ "$(usage_commits)" = "$USAGE_SEED_COMMITS" ] || { echo "the agent changed an empty KB it was only asked about"; exit 1; }
[ -s "$USAGE_RESULT" ] || { echo "the agent gave no answer"; exit 1; }
