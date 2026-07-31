#!/bin/sh
# scenarios/03_running_service_invokes_repair.sh — updating over an older
# installed binary must invoke upgrade-repair on the newly installed binary
# (it owns detecting/replacing a running service; install.sh no longer probes
# status itself).

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="03_running_service_invokes_repair"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v3.0.0"
FAKE_REPAIR_EXIT="0"
write_fake_binary "$DEST" "v2.9.0"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install update

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "0" "install.sh exits 0 when repair succeeds"
assert_file_contains "$FAKE_BIN_LOG" "upgrade-repair" "upgrade-repair is invoked on the newly installed binary"

install_report "$SCENARIO_NAME"
exit $?
