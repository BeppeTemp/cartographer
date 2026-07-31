#!/bin/sh
# scenarios/07_unexpected_exit_diagnostic.sh — an upgrade-repair exit code
# outside {0,1,2} is neither silently ignored nor treated as success: it is
# a hard, explicit diagnostic failure.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="07_unexpected_exit_diagnostic"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v7.0.0"
FAKE_REPAIR_EXIT="42"
write_fake_binary "$DEST" "v6.9.0"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install update

echo "--- Assertions ---"
if [ "$INSTALL_RC" -eq 0 ]; then
    _assert_fail "install.sh must not exit 0 on an unrecognized upgrade-repair exit code"
else
    _assert_pass "install.sh exits non-zero (got ${INSTALL_RC})"
fi
assert_contains "$INSTALL_OUTPUT" "unexpected exit code 42" "prints a clear diagnostic naming the unexpected exit code"

install_report "$SCENARIO_NAME"
exit $?
