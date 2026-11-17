#!/usr/bin/env bash
# The menu-bar icon under a real window server (D1493): `cartographer tray` adds
# its status item, applies the snapshots it polls (#2183: a delivery lost before
# the run loop left the menu on its placeholder), stays alive, and leaves
# cleanly on SIGTERM. It touches no launchd job and no real server: a scratch
# HOME holds a client config pointing at a fake /health on a free port.
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
fake=""
trap '[ -z "$pid" ] || kill "$pid" 2>/dev/null || true; [ -z "$fake" ] || kill "$fake" 2>/dev/null || true' EXIT

port=$(mac_free_port)
mkdir -p "$work/home"
printf 'server_url: http://127.0.0.1:%s/mcp\n' "$port" >"$work/home/.cartographer.yaml"
python3 - "$port" >/dev/null 2>&1 <<'PY' &
import http.server, sys
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        b = b'{"status":"ok","version":"9.9.9","ready":true,"kbs":[{"name":"kb-a"}]}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)
    def log_message(self, *a):
        pass
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY
fake=$!

HOME="$work/home" "$BIN" tray --interval 1s --log-file "$work/tray.log" >"$work/out.log" 2>&1 &
pid=$!
sleep 3
kill -0 "$pid" 2>/dev/null || { cat "$work/out.log" "$work/tray.log" >&2 2>/dev/null || true; mac_fail "tray exited within 3s"; }
mac_ok "tray stays alive for 3s"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  grep -q 'tray: menu shows state running' "$work/tray.log" && break
  sleep 1
done
grep -q 'tray: state running version "9.9.9"' "$work/tray.log" || { cat "$work/tray.log" >&2; mac_fail "the poll never saw the fake server running"; }
grep -q 'tray: menu shows state running' "$work/tray.log" || { cat "$work/tray.log" >&2; mac_fail "the menu never showed the running snapshot"; }
mac_ok "a polled snapshot reaches the menu"

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
