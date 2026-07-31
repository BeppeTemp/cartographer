#!/bin/sh
# scenarios/02_first_install_no_service.sh — fresh install, no prior binary
# and no native service: upgrade-repair runs as an idempotent no-op (exit 0)
# and the install succeeds.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="02_first_install_no_service"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
FAKE_REPAIR_EXIT="0"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"
# No pre-existing $DEST: this is a first install.

run_install install

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "0" "install.sh exits 0 on a fresh install with no native service"
assert_executable "$DEST" "the binary is installed"
assert_file_contains "$FAKE_BIN_LOG" "upgrade-repair" "upgrade-repair is invoked after a fresh install"

install_report "$SCENARIO_NAME"
exit $?
