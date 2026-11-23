#!/usr/bin/env bash
# A page about the review exists and ends done, not open.
. "$(dirname "$0")/../lib.sh"
usage_lint_clean || exit 1
hit=$(grep -rliE 'rotation' "$USAGE_KB/data" --include='*.md' | grep -v 'dev/token-rotation.md' | grep -v '/log.md$' | grep -v '/index.md$' | head -n 1)
if [ -n "$hit" ]; then
  grep -qE '^status: *done' "$hit" || { echo "the task page ${hit#"$USAGE_KB"/} is not done"; exit 1; }
else
  grep -qE '^status: *done' "$USAGE_KB/data/dev/token-rotation.md" || { echo "no task page was opened and closed"; exit 1; }
fi
