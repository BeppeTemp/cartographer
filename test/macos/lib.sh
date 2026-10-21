#!/usr/bin/env bash
# Shared by test/macos/*.sh (D997): bash 3.2 (what macOS ships), sourced.
#
# These scripts register a REAL launchd job and, for the Cask, install a real
# Homebrew package. The service code registers only when $HOME is the account's
# own home (internal/service/homeguard.go, #683), so they run with the runner's
# real HOME, never a sandbox one, and refuse to run anywhere that is not a CI
# runner unless CARTOGRAPHER_MACOS_E2E=1: on a developer's Mac the label
# com.cartographer.serve is the developer's own service.

mac_guard() {
  [ "$(uname -s)" = Darwin ] || { echo "test/macos: macOS only" >&2; exit 2; }
  if [ -z "${CI:-}" ] && [ "${CARTOGRAPHER_MACOS_E2E:-}" != 1 ]; then
    echo "test/macos: refusing to run outside CI: this registers a real launchd job (com.cartographer.serve) and would replace yours. Set CARTOGRAPHER_MACOS_E2E=1 on a throwaway machine." >&2
    exit 2
  fi
}

mac_fail() { echo "FAIL: $*" >&2; exit 1; }
mac_ok() { echo "  ok: $*"; }
MAC_DONE=0
mac_pass() { MAC_DONE=1; echo "$1: PASS"; }

# mac_workdir <name>: a fresh directory under RUNNER_TEMP (or TMPDIR).
mac_workdir() {
  local d
  d=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/cartographer-macos.$1.XXXXXX") || exit 2
  echo "$d"
}

mac_free_port() {
  python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

# Absolute: install.sh scenarios put a fake curl first on PATH.
MAC_CURL=/usr/bin/curl

# mac_wait_ready <port> [seconds]: /health answers ready:true.
mac_wait_ready() {
  local port=$1 secs=${2:-30} i=0 body=
  while [ "$i" -lt "$secs" ]; do
    body=$($MAC_CURL -fsS --max-time 2 "http://127.0.0.1:$port/health" 2>/dev/null || true)
    case $body in *'"ready":true'*) return 0 ;; esac
    sleep 1
    i=$((i + 1))
  done
  echo "health after ${secs}s: ${body:-<no answer>}" >&2
  return 1
}

mac_health_version() { # <port>
  $MAC_CURL -fsS --max-time 2 "http://127.0.0.1:$1/health" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p'
}

MAC_LABEL=com.cartographer.serve
MAC_PLIST="$HOME/Library/LaunchAgents/$MAC_LABEL.plist"
MAC_LOG="$HOME/Library/Logs/cartographer/server.log"

mac_launchd_print() { launchctl print "gui/$(id -u)/$MAC_LABEL"; }
mac_launchd_pid() { mac_launchd_print 2>/dev/null | sed -n 's/^[[:space:]]*pid = \([0-9][0-9]*\).*/\1/p' | head -n 1; }

# mac_wait_for <seconds> <command...>: poll until the command succeeds.
mac_wait_for() {
  local secs=$1 i=0
  shift
  while [ "$i" -lt "$secs" ]; do
    "$@" >/dev/null 2>&1 && return 0
    sleep 1
    i=$((i + 1))
  done
  return 1
}

# mac_probe_domain: the per-user launchd domain exists. The hosted image logs
# the `runner` user in; if this ever fails, report it on the plan (#997)
# instead of adding a fallback.
mac_probe_domain() {
  launchctl print "gui/$(id -u)" >/dev/null 2>&1 \
    || mac_fail "launchd domain gui/$(id -u) does not exist on this runner: the per-user service cannot be registered here (report on #997)"
}

# mac_cleanup <bin>: on any exit, leave no job, plist or stray server behind;
# on failure print what launchd and the server say.
mac_cleanup() {
  local rc=$? bin=${1:-}
  trap - EXIT
  # A shell that dies on an expansion error can leave $? at 0 in an EXIT trap
  # (bash 3.2): only a script that reached mac_pass passes.
  if [ "$rc" -eq 0 ] && [ "${MAC_DONE:-0}" != 1 ]; then
    echo "FAIL: the script ended before its last line" >&2
    rc=1
  fi
  if [ "$rc" -ne 0 ]; then
    echo "--- launchctl print" >&2
    mac_launchd_print >&2 2>&1 || true
    echo "--- $MAC_LOG" >&2
    tail -n 50 "$MAC_LOG" >&2 2>&1 || true
  fi
  if [ -n "$bin" ] && [ -x "$bin" ]; then
    "$bin" service sync-timer uninstall >/dev/null 2>&1 || true
    "$bin" service uninstall >/dev/null 2>&1 || true
  fi
  launchctl bootout "gui/$(id -u)/$MAC_LABEL" >/dev/null 2>&1 || true
  rm -f "$MAC_PLIST"
  exit "$rc"
}
