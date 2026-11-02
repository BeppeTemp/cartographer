#!/usr/bin/env bash
# The menu-bar icon under a real window server (D1486): `cartographer tray` adds
# its status item, stays alive, and leaves cleanly on SIGTERM. It touches no
# launchd job and no server: with nothing installed the icon shows "not installed".
#
# Usage: test/macos/tray.sh <cartographer binary>      (CI, or CARTOGRAPHER_MACOS_E2E=1)
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=lib.sh
. "$here/lib.sh"
mac_guard

BIN=${1:-${BIN:-}}
[ -x "$BIN" ] || { echo "usage: test/macos/tray.sh <cartographer binary>" >&2; exit 2; }

work=$(mac_workdir tray)
pid=""
trap '[ -z "$pid" ] || kill "$pid" 2>/dev/null || true' EXIT

"$BIN" tray --interval 1s --log-file "$work/tray.log" >"$work/out.log" 2>&1 &
pid=$!
sleep 3
kill -0 "$pid" 2>/dev/null || { cat "$work/out.log" "$work/tray.log" >&2 2>/dev/null || true; mac_fail "tray exited within 3s"; }
mac_ok "tray stays alive for 3s"

kill -TERM "$pid"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  kill -0 "$pid" 2>/dev/null || break
  sleep 1
done
if kill -0 "$pid" 2>/dev/null; then mac_fail "tray still running 10s after SIGTERM"; fi
rc=0
wait "$pid" || rc=$?
pid=""
[ "$rc" -eq 0 ] || mac_fail "tray exited $rc after SIGTERM, want 0"
mac_ok "SIGTERM: clean exit"
mac_pass tray
