#!/bin/sh
# scenarios/04_stopped_absent_stays_stopped.sh — install.sh must never call
# `service start`/`service restart` itself: upgrade-repair alone decides
# whether a native service is touched, and a deliberately stopped or absent
# service stays that way (WP3 removes install.sh's own status probe/restart).

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="04_stopped_absent_stays_stopped"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v4.0.0"
# upgrade-repair itself reports 0: stopped/absent-service repair is a
# successful no-op (WP2 state matrix) — install.sh must not second-guess it
# by separately probing/starting the service.
FAKE_REPAIR_EXIT="0"
write_fake_binary "$DEST" "v3.9.0"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install update

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "0" "install.sh exits 0"
assert_file_not_contains "$FAKE_BIN_LOG" "service start" "install.sh never calls 'service start' directly"
assert_file_not_contains "$FAKE_BIN_LOG" "service restart" "install.sh never calls 'service restart' directly"
assert_not_contains "$INSTALL_OUTPUT" "restarting cartographer service" "install.sh no longer announces a direct restart"

install_report "$SCENARIO_NAME"
exit $?
