#!/bin/sh
# scenarios/06_exit2_fails_visibly_after_install.sh — upgrade-repair exit 2
# means the running native service could not be safely replaced/verified:
# `install.sh update` must fail visibly, but only after the new binary has
# already been installed (the binary swap itself is not rolled back).

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="06_exit2_fails_visibly_after_install"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v6.0.0"
FAKE_REPAIR_EXIT="2"
write_fake_binary "$DEST" "v5.9.0"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install update

echo "--- Assertions ---"
if [ "$INSTALL_RC" -eq 0 ]; then
    _assert_fail "install.sh must not exit 0 when the running service could not be verified"
else
    _assert_pass "install.sh exits non-zero (got ${INSTALL_RC})"
fi
assert_executable "$DEST" "the new binary was installed before the failure was reported"
assert_contains "$INSTALL_OUTPUT" "could not be verified" "reports that the running service could not be verified"

install_report "$SCENARIO_NAME"
exit $?
