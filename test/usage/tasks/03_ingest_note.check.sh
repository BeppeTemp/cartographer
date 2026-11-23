#!/usr/bin/env bash
# The new fact is in the KB, the old page it changes was not left contradicting it, links hold.
. "$(dirname "$0")/../lib.sh"
usage_lint_clean || exit 1
[ "$(usage_commits)" -gt "$USAGE_SEED_COMMITS" ] || { echo "nothing was written"; exit 1; }
grep -rqiE 'forty-five|45' "$USAGE_KB/data" || { echo "the new retention (forty-five days) is in no page"; exit 1; }
grep -qE '^# Backup policy' "$USAGE_KB/data/ops/backup-policy.md" || { echo "the backup policy page is gone"; exit 1; }
