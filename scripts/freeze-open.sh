#!/usr/bin/env bash
# freeze-open.sh — is the weekly freeze window open? (D956, D989, D1180)
#
# Exit 0 while it is open (Saturday 00:00 to Monday 08:30, Europe/Rome), 1 when
# it is closed. The window is the one release-train.yml's freeze check counts
# from ("last Saturday 00:00 in Rome"): a bot that lands work on main must not
# land it in this window. FREEZE_NOW=<epoch seconds> replaces the clock (tests).
set -euo pipefail

now=${FREEZE_NOW:-$(date +%s)}
# GNU date takes -d @N, BSD date -r N.
if ! parts=$(TZ=Europe/Rome date -d "@$now" '+%u %H%M' 2>/dev/null); then
  parts=$(TZ=Europe/Rome date -r "$now" '+%u %H%M')
fi
read -r dow hm <<<"$parts"

# %u: 1 = Monday ... 6 = Saturday, 7 = Sunday. 10# keeps 0830 from reading as octal.
case "$dow" in
  6 | 7) exit 0 ;;
  1) [ $((10#$hm)) -lt 830 ] && exit 0 ;;
esac
exit 1
