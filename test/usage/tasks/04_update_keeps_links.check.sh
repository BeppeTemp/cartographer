#!/usr/bin/env bash
# The page says the new name, still exists at its id, and nothing that points at it broke.
. "$(dirname "$0")/../lib.sh"
text=$(usage_call concept_read '{"id":"dev/session-store"}') || { echo "dev/session-store cannot be read any more: $text"; exit 1; }
grep -q 'sessions_v2' <<<"$text" || { echo "dev/session-store does not say sessions_v2"; exit 1; }
grep -q 'session-store' "$USAGE_KB/data/dev/auth-flow.md" || { echo "dev/auth-flow lost its link to the session store"; exit 1; }
usage_lint_clean
