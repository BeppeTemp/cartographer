#!/bin/sh
# scenarios/05_exit1_binary_update_succeeds.sh — upgrade-repair exit 1 means
# the native service was replaced/verified but provider sync is pending; the
# binary update itself must still be reported as a success, with the exact
# retry command printed.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="05_exit1_binary_update_succeeds"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v5.0.0"
FAKE_REPAIR_EXIT="1"
write_fake_binary "$DEST" "v4.9.0"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install update

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "0" "install.sh still exits 0 — the binary update succeeded"
assert_executable "$DEST" "the new binary remains installed"
assert_contains "$INSTALL_OUTPUT" "provider sync is pending" "logs that provider sync is pending"
assert_contains "$INSTALL_OUTPUT" "retry with: ${DEST} sync" "prints the exact retry command"

install_report "$SCENARIO_NAME"
exit $?
