#!/usr/bin/env bash
# The native service under the real launchd (D997): the plist is accepted, the
# job runs and answers, restart/stop/uninstall do what status says. The Go
# tests drive the same code through a stub runner; this is the only place the
# launchctl calls are real.
#
# Usage: test/macos/service.sh <cartographer binary>      (CI, or CARTOGRAPHER_MACOS_E2E=1)
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=lib.sh
. "$here/lib.sh"
mac_guard

BIN=${1:-${BIN:-}}
[ -x "$BIN" ] || { echo "usage: test/macos/service.sh <cartographer binary>" >&2; exit 2; }

work=$(mac_workdir service)
trap 'mac_cleanup "$BIN"' EXIT
port=$(mac_free_port)

echo "→ install"
mac_probe_domain
# serve never creates a KB (D906), so one exists before the service starts.
"$BIN" kb create smoke --no-remote --local --data "$work/data" >/dev/null
"$BIN" service install --config "$work/server.yaml" --data "$work/data" --http "127.0.0.1:$port" >/dev/null
plutil -lint "$MAC_PLIST" >/dev/null || mac_fail "plutil rejects $MAC_PLIST"
mac_ok "plist passes plutil -lint"
mac_launchd_running || mac_fail "launchctl print: job is not running"
mac_ok "launchctl print: state = running"
mac_wait_ready "$port" 30 || mac_fail "/health never became ready"
mac_ok "/health ready"
status=$("$BIN" service status --config "$work/server.yaml") || mac_fail "service status exited $?"
case $status in *"$BIN"*) ;; *) mac_fail "status does not name the binary: $status" ;; esac
case $status in *"$work/server.yaml"*) ;; *) mac_fail "status does not name the config: $status" ;; esac
mac_ok "service status exits 0 and names the binary and config"

echo "→ restart --wait"
pid_before=$(mac_launchd_pid)
"$BIN" service restart --wait --config "$work/server.yaml" >/dev/null || mac_fail "restart --wait failed"
mac_launchd_running || mac_fail "not running after restart"
mac_wait_ready "$port" 30 || mac_fail "/health not ready after restart"
[ "$(mac_launchd_pid)" != "$pid_before" ] || mac_fail "restart --wait kept pid $pid_before"
mac_ok "restart --wait: same label serving, new pid"

echo "→ stop and start"
# launchd keeps the job registered (disabled) after a stop; `status` must still
# tell it from a running one: exit 3, installed but stopped (#1006).
"$BIN" service stop >/dev/null || mac_fail "stop failed"
serving() { $MAC_CURL -fsS --max-time 2 "http://127.0.0.1:$port/health" >/dev/null 2>&1; }
not_serving() { ! serving; }
mac_wait_for 30 not_serving || mac_fail "/health still answers 30s after stop"
mac_ok "stopped: /health no longer answers"
rc=0
"$BIN" service status --config "$work/server.yaml" >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 3 ] || mac_fail "status after stop exited $rc, want 3"
mac_ok "stopped: service status exits 3"
"$BIN" service start >/dev/null || mac_fail "start failed"
mac_wait_ready "$port" 30 || mac_fail "/health not ready after start"
mac_ok "start: serving again"

echo "→ uninstall"
"$BIN" service uninstall >/dev/null || mac_fail "uninstall failed"
[ ! -e "$MAC_PLIST" ] || mac_fail "plist still present"
if mac_launchd_print >/dev/null 2>&1; then mac_fail "launchctl print still finds the job"; fi
rc=0
"$BIN" service status --config "$work/server.yaml" >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 4 ] || mac_fail "status after uninstall exited $rc, want 4"
mac_ok "uninstalled: no plist, no job, status exits 4"

echo "→ sync timer"
"$BIN" service sync-timer install --interval 1h >/dev/null || mac_fail "sync-timer install failed"
sync_plist="$HOME/Library/LaunchAgents/com.cartographer.sync.plist"
plutil -lint "$sync_plist" >/dev/null || mac_fail "plutil rejects $sync_plist"
grep -q 'com.cartographer.sync' "$sync_plist" || mac_fail "sync plist lacks its label"
"$BIN" service sync-timer uninstall >/dev/null || mac_fail "sync-timer uninstall failed"
[ ! -e "$sync_plist" ] || mac_fail "sync plist still present"
mac_ok "sync timer plist installed and removed"

mac_pass service.sh
