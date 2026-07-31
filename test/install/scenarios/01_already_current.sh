#!/bin/sh
# scenarios/01_already_current.sh — the installed version already equals the
# latest release tag: install.sh must return early (D95 early-return, WP3
# preserves it) and never invoke upgrade-repair.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="01_already_current"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v1.2.3"
write_fake_binary "$DEST" "$FAKE_TAG"

run_install update

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "0" "install.sh exits 0 when already current"
assert_contains "$INSTALL_OUTPUT" "already installed" "prints the already-installed message"
assert_file_not_contains "$FAKE_BIN_LOG" "upgrade-repair" "upgrade-repair is not invoked (early return)"

install_report "$SCENARIO_NAME"
exit $?
