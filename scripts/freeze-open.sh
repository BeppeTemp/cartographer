#!/usr/bin/env bash
# freeze-open.sh — is the weekly freeze open? (D956, D1180, D1192)
#
# Exit 0 while it is open, 1 when it is closed. Open from Wednesday 00:00
# Europe/Rome until Thursday's train has run: Thursday before 18:00, and after that
# until the day's two `schedule` runs of release-train.yml are both completed (the
# cron often starts late, and a merge before the train skips it). The same rule
# as .claude/skills/cartographer-implement-plan/SKILL.md §4. A run list that
# cannot be read counts as open.
#
# Tests inject the inputs: FREEZE_NOW=<epoch seconds> replaces the clock and
# FREEZE_RUNS="<status> <status> ..." the statuses of today's scheduled runs.
set -euo pipefail

now=${FREEZE_NOW:-$(date +%s)}
# GNU date takes -d @N, BSD date -r N.
fmt() { # fmt <TZ> <format>
  TZ=$1 date -d "@$now" "+$2" 2>/dev/null || TZ=$1 date -r "$now" "+$2"
}
read -r dow hm <<<"$(fmt Europe/Rome '%u %H%M')"

# %u: 1 = Monday ... 3 = Wednesday, 4 = Thursday. 10# keeps 0930 from reading as octal.
case "$dow" in
  3) exit 0 ;;
  4) [ $((10#$hm)) -lt 1800 ] && exit 0 ;;
  *) exit 1 ;;
esac

# Thursday after 18:00: open until two scheduled train runs created today (UTC) are completed.
if [ -z "${FREEZE_RUNS+x}" ]; then
  FREEZE_RUNS=$(gh run list --workflow release-train.yml --event schedule \
    --created "$(fmt UTC %F)" --json status --jq '.[].status') || {
    echo "freeze-open: cannot list the release-train runs (needs GH_TOKEN, GH_REPO, actions: read): freeze counted as open" >&2
    exit 0
  }
fi
total=0
for s in $FREEZE_RUNS; do
  total=$((total + 1))
  [ "$s" = completed ] || exit 0
done
[ "$total" -ge 2 ] && exit 1
exit 0
