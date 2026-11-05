#!/bin/sh
# Runs INSIDE a container with no systemd (test/linux/run.sh): `cartographer
# setup` and the service verbs on the cron backend (D1682), against a real cron
# daemon. POSIX sh (busybox ash and dash). The Go tests drive the same code
# through a stubbed crontab; this is the only place the daemon is real.
#
# Usage: nosystemd.sh cron|nocron      (as a non-root account, HOME its real home)
set -eu

mode=${1:-}
BIN=cartographer
state="$HOME/.local/state/cartographer"
seed='0 3 * * * echo foreign-line'

fail() {
  echo "FAIL: $*" >&2
  echo "-- uname: $(uname -m); crontab: $(command -v crontab || echo none)" >&2
  for f in server.log supervisor.log; do
    [ -f "$state/$f" ] && { echo "-- $f" >&2; tail -n 40 "$state/$f" >&2; }
  done
  exit 1
}
ok() { echo "  ok: $*"; }

health() { curl -fsS --max-time 2 http://127.0.0.1:39273/health 2>/dev/null; }
started_at() { health | sed -n 's/.*"started_at":"\([^"]*\)".*/\1/p'; }
wait_for() { # seconds command...
  n=$1; shift
  while [ "$n" -gt 0 ]; do "$@" && return 0; sleep 1; n=$((n - 1)); done
  return 1
}
serving() { health >/dev/null; }
not_serving() { ! serving; }
# procps is not in every image: read /proc.
serve_pids() {
  for f in /proc/[0-9]*/cmdline; do
    if tr '\0' ' ' <"$f" 2>/dev/null | grep -q 'cartographer serve'; then
      p=${f#/proc/}; echo "${p%%/*}"
    fi
  done
}
count_serve() { serve_pids | wc -l | tr -d ' '; }
kill_serve() { # signal
  # shellcheck disable=SC2046
  kill "-$1" $(serve_pids) 2>/dev/null || true
}
status_is() { # code
  rc=0; $BIN service status >/dev/null 2>&1 || rc=$?
  [ "$rc" -eq "$1" ]
}
# The server block's command, taken verbatim: the block is selected by its
# markers, then the line that is neither a comment nor @reboot, minus its five
# schedule fields (the interval is not this test's business).
watchdog() {
  cmd=$(crontab -l | sed -n '/^# BEGIN cartographer server/,/^# END cartographer server/p' \
    | awk '!/^#/ && !/^@/ { for (i = 6; i <= NF; i++) printf "%s%s", $i, (i < NF ? " " : ""); printf "\n" }')
  [ -n "$cmd" ] || fail "no watchdog command in the server block"
  sh -c "$cmd"
}
blocks() { crontab -l | grep -c "^# BEGIN cartographer $1"; }

git config --global user.name ci
git config --global user.email ci@local
git config --global init.defaultBranch main
# A provider that needs no installed client: its config directory is enough.
mkdir -p "$HOME/.claude"

case $mode in
cron)
  echo "→ setup on cron"
  printf '%s\n' "$seed" >"$HOME/seed.cron"
  crontab "$HOME/seed.cron"
  out=$($BIN setup --yes --no-input --no-remote --name kb-a --agents claude 2>&1) || fail "setup exited $?: $out"
  echo "$out"
  case $out in *"cron:"*) ;; *) fail "the plan line does not name cron" ;; esac
  case $out in *"server ready"*) ;; *) fail "setup did not end with 'server ready'" ;; esac
  ok "setup exit 0, plan names cron, server ready"

  status_is 0 || fail "service status is not 0"
  serving || fail "/health does not answer"
  [ "$(crontab -l | grep -c 'BEGIN cartographer server')" -eq 1 ] || fail "not exactly one server block"
  crontab -l | grep -qxF "$seed" || fail "the foreign crontab line is gone"
  ok "status 0, /health, one block, foreign line intact"

  echo "→ setup rerun over the managed server"
  out=$($BIN setup --yes --no-input --no-remote --name kb-a --agents claude 2>&1) || fail "setup rerun exited $?: $out"
  case $out in *"already running ("*) ;; *) fail "the rerun plan does not keep the running server: $out" ;; esac
  case $out in *"server ready"*) ;; *) fail "the rerun did not end with 'server ready'" ;; esac
  [ "$(blocks server)" -eq 1 ] || fail "the rerun left $(blocks server) server blocks"
  ok "rerun keeps the server, still one block"

  echo "→ sync timer on cron"
  $BIN service sync-timer install >/dev/null || fail "sync-timer install failed"
  [ "$(blocks sync)" -eq 1 ] || fail "want one sync block, found $(blocks sync)"
  $BIN service sync-timer install >/dev/null || fail "sync-timer reinstall failed"
  [ "$(blocks sync)" -eq 1 ] || fail "a reinstall left $(blocks sync) sync blocks"
  [ "$(blocks server)" -eq 1 ] || fail "the sync timer touched the server block"
  crontab -l | grep -qxF "$seed" || fail "the sync timer touched the foreign line"
  $BIN service sync-timer status >/dev/null || fail "sync-timer status is not 0"
  $BIN service sync-timer uninstall >/dev/null || fail "sync-timer uninstall failed"
  [ "$(blocks sync)" -eq 0 ] || fail "the sync block is still in the crontab"
  [ "$(blocks server)" -eq 1 ] || fail "removing the sync timer removed the server block"
  crontab -l | grep -qxF "$seed" || fail "the foreign line is gone after the sync timer's removal"
  ok "one sync block, idempotent, removed alone, seed and server block intact"

  echo "→ watchdog"
  t0=$(started_at)
  kill_serve 9
  wait_for 20 not_serving || fail "server survived kill -9"
  watchdog
  wait_for 30 serving || fail "the watchdog did not bring the server back"
  [ "$(started_at)" != "$t0" ] || fail "started_at unchanged after the watchdog"
  ok "watchdog restarted a killed server (new started_at)"
  kill_serve 9
  wait_for 20 not_serving || fail "server survived kill -9"
  watchdog & watchdog & wait
  wait_for 30 serving || fail "concurrent watchdogs left no server"
  sleep 2
  [ "$(count_serve)" -eq 1 ] || fail "concurrent watchdogs left $(count_serve) servers"
  ok "two concurrent watchdogs, one process"

  echo "→ stop, start, restart"
  $BIN service stop >/dev/null || fail "stop failed"
  wait_for 40 not_serving || fail "still serving after stop"
  wait_for 40 status_is 3 || fail "status is not 3 after stop"
  watchdog; sleep 2
  not_serving || fail "the watchdog restarted a stopped server"
  status_is 3 || fail "status left 3 after a watchdog tick"
  ok "stopped: status 3, the watchdog does nothing"
  $BIN service start >/dev/null || fail "start failed"
  wait_for 30 serving || fail "not serving after start"
  status_is 0 || fail "status is not 0 after start"
  t1=$(started_at)
  $BIN service restart --wait >/dev/null || fail "restart --wait failed"
  wait_for 30 serving || fail "not serving after restart"
  [ "$(started_at)" != "$t1" ] || fail "restart --wait kept started_at"
  ok "start 0, restart --wait: new started_at"

  echo "→ uninstall"
  kbdir=$(find "$HOME" -type d -name kb-a -not -path "$HOME/.claude/*" | head -n 1)
  [ -n "$kbdir" ] || fail "no kb-a directory before uninstall"
  $BIN service uninstall >/dev/null || fail "uninstall failed"
  wait_for 40 not_serving || fail "still serving after uninstall"
  status_is 4 || fail "status is not 4 after uninstall"
  [ "$(crontab -l)" = "$seed" ] || fail "crontab is not the pre-seeded text: $(crontab -l)"
  [ "$(count_serve)" -eq 0 ] || fail "a server process is left"
  [ -d "$kbdir" ] || fail "the KB directory is gone"
  ok "status 4, crontab restored, no process, KB untouched"
  ;;

nocron)
  echo "→ no crontab, no server"
  rc=0
  out=$($BIN setup --yes --no-input --no-remote --name kb-a --agents claude 2>&1) || rc=$?
  echo "$out"
  [ "$rc" -eq 2 ] || fail "setup exited $rc, wanted 2"
  case $out in *"service install --config-only"*) ;; *) fail "the manual route is not printed" ;; esac
  [ ! -e "$HOME/.config/cartographer" ] || fail "setup wrote ~/.config/cartographer"
  [ -z "$(find "$HOME" -type d -name kb-a -not -path "$HOME/.claude/*")" ] || fail "setup created a data dir"
  ok "exit 2, manual route printed, nothing written"

  echo "→ the manual route"
  $BIN service install --config-only >/dev/null || fail "service install --config-only failed"
  cfg="$HOME/.config/cartographer/server.yaml"
  start_server() {
    mkdir -p "$state"
    nohup $BIN serve --config "$cfg" --log-file "$state/server.log" >/dev/null 2>&1 &
    wait_for 30 serving || fail "the hand-started server does not answer"
  }
  start_server
  rc=0
  out=$($BIN setup --yes --no-input --no-remote --name kb-a --agents claude 2>&1) || rc=$?
  echo "$out"
  [ "$rc" -eq 1 ] || fail "setup over a hand-started server exited $rc, wanted 1 (restart needed)"
  case $out in *"already answering on 127.0.0.1:39273 (not managed by Cartographer) - keep"*) ;; *) fail "the plan does not keep the hand-started server" ;; esac
  case $out in *"already answering (not managed by Cartographer)"*) ;; *) fail "the server step does not report the unmanaged server" ;; esac
  case $out in *"restart it by hand"*) ;; *) fail "setup did not ask for a restart" ;; esac
  kill_serve 15
  wait_for 40 not_serving || fail "the hand-started server did not stop"
  start_server
  out=$($BIN setup --yes --no-input --no-remote --name kb-a --agents claude 2>&1) || fail "the final setup exited $?: $out"
  case $out in *"server ready"*) ;; *) fail "the final setup did not end with 'server ready'" ;; esac
  ok "manual route ends with server ready"
  ;;

*) echo "usage: nosystemd.sh cron|nocron" >&2; exit 2 ;;
esac
echo "PASS ($mode)"
