#!/usr/bin/env bash
# install.sh against two real binaries and a real running service (D997): the
# `upgrade-repair` call install.sh makes after an update (install.sh:146) is
# otherwise only ever seen by a fake binary. The release list and the asset come
# from test/install/lib/fake-curl.sh, as in the ubuntu suite; the network is
# never touched.
#
# Usage: test/macos/install.sh <old binary, v9.9.8> <new binary, v9.9.9>
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
# shellcheck source=lib.sh
. "$here/lib.sh"
mac_guard

OLD=${1:-}
NEW=${2:-}
[ -x "$OLD" ] && [ -x "$NEW" ] || { echo "usage: test/macos/install.sh <old binary> <new binary>" >&2; exit 2; }

work=$(mac_workdir install)
install_dir="$work/install"
bin="$install_dir/cartographer"
mkdir -p "$work/fakebin" "$install_dir"
trap 'mac_cleanup "$bin"' EXIT

cp "$repo/test/install/lib/fake-curl.sh" "$work/fakebin/curl"
chmod +x "$work/fakebin/curl"

# run_installer <subcommand...>: install.sh with the fake curl first on PATH.
run_installer() {
  PATH="$work/fakebin:$PATH" CARTOGRAPHER_INSTALL_DIR="$install_dir" SCENARIO_TMP="$work" \
    FAKE_TAG="${FAKE_TAG:-v9.9.9}" FAKE_NEW_BINARY="${FAKE_NEW_BINARY:-$NEW}" FAKE_SHA_MODE="${FAKE_SHA_MODE:-valid}" \
    sh "$repo/install.sh" "$@"
}

mac_probe_domain
port=$(mac_free_port)

echo "→ install v9.9.8"
FAKE_TAG=v9.9.8 FAKE_NEW_BINARY=$OLD run_installer install >"$work/out.log" 2>&1 || { cat "$work/out.log" >&2; mac_fail "install.sh install failed"; }
[ "$("$bin" version)" = v9.9.8 ] || mac_fail "installed version is $("$bin" version), want v9.9.8"
mac_ok "installed binary reports v9.9.8"

"$bin" kb create smoke --no-remote --local --data "$work/data" >/dev/null
"$bin" service install --config "$work/server.yaml" --data "$work/data" --http "127.0.0.1:$port" >/dev/null
mac_wait_ready "$port" 30 || mac_fail "service not ready"
[ "$(mac_health_version "$port")" = v9.9.8 ] || mac_fail "/health reports $(mac_health_version "$port"), want v9.9.8"
pid_before=$(mac_launchd_pid)
mac_ok "service running v9.9.8 (pid $pid_before)"

echo "→ checksum mismatch leaves the old binary and the service"
FAKE_TAG=v9.9.9 FAKE_NEW_BINARY=$NEW FAKE_SHA_MODE=mismatch run_installer update >"$work/out.log" 2>&1 && mac_fail "update with a bad checksum succeeded"
[ "$("$bin" version)" = v9.9.8 ] || mac_fail "binary changed despite the checksum mismatch"
[ "$(mac_launchd_pid)" = "$pid_before" ] || mac_fail "service restarted despite the checksum mismatch"
mac_wait_ready "$port" 10 || mac_fail "service not serving after the refused update"
mac_ok "refused update: v9.9.8 still installed, same pid, still serving"

echo "→ update to v9.9.9 replaces the running service"
FAKE_TAG=v9.9.9 FAKE_NEW_BINARY=$NEW FAKE_SHA_MODE=valid run_installer update >"$work/out.log" 2>&1 || { cat "$work/out.log" >&2; mac_fail "install.sh update failed"; }
[ "$("$bin" version)" = v9.9.9 ] || mac_fail "binary is $("$bin" version), want v9.9.9"
mac_wait_ready "$port" 30 || mac_fail "service not ready after update"
[ "$(mac_health_version "$port")" = v9.9.9 ] || mac_fail "/health reports $(mac_health_version "$port") after update, want v9.9.9: upgrade-repair did not replace the service"
[ "$(mac_launchd_pid)" != "$pid_before" ] || mac_fail "launchd pid unchanged ($pid_before) after update"
mac_ok "/health reports v9.9.9, new pid $(mac_launchd_pid)"

echo "→ update with a stopped service leaves it stopped (#1006)"
# The "update" goes back to v9.9.8: install.sh short-circuits on the same tag, and
# only a changed binary reaches upgrade-repair. Stop leaves the job registered
# but disabled; repair must not try to signal it (a timeout, exit 2).
"$bin" service stop >/dev/null || mac_fail "service stop failed"
# The process drains after SIGTERM and a live pid reads as running: wait for exit 3.
status_stopped() { "$bin" service status --config "$work/server.yaml" >/dev/null 2>&1; [ $? -eq 3 ]; }
mac_wait_for 30 status_stopped || mac_fail "status never exited 3 after stop"
FAKE_TAG=v9.9.8 FAKE_NEW_BINARY=$OLD FAKE_SHA_MODE=valid run_installer update >"$work/out.log" 2>&1 || { cat "$work/out.log" >&2; mac_fail "install.sh update failed with the service stopped"; }
[ "$("$bin" version)" = v9.9.8 ] || mac_fail "binary is $("$bin" version), want v9.9.8"
rc=0
"$bin" service status --config "$work/server.yaml" >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 3 ] || mac_fail "status after update exited $rc, want 3: repair started or lost the stopped service"
if $MAC_CURL -fsS --max-time 2 "http://127.0.0.1:$port/health" >/dev/null 2>&1; then mac_fail "service answers after an update that should leave it stopped"; fi
mac_ok "update with the service stopped: succeeded, still stopped"

echo "→ uninstall refuses while units are installed"
rc=0
run_installer uninstall >"$work/out.log" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || mac_fail "install.sh uninstall succeeded with the service installed"
[ -x "$bin" ] || mac_fail "uninstall removed the binary despite refusing"
grep -q "$MAC_LABEL.plist" "$work/out.log" || mac_fail "refusal does not name the plist"
"$bin" service uninstall >/dev/null || mac_fail "service uninstall failed"
FAKE_TAG=v9.9.9 FAKE_NEW_BINARY=$NEW run_installer uninstall >"$work/out.log" 2>&1 || { cat "$work/out.log" >&2; mac_fail "uninstall after service uninstall failed"; }
[ ! -e "$bin" ] || mac_fail "binary still present after uninstall"
mac_ok "refused with units installed, succeeded after service uninstall"

mac_pass install.sh
