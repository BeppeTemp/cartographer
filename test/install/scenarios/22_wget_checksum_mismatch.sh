#!/bin/sh
# scenarios/22_wget_checksum_mismatch.sh — a checksum mismatch through wget installs nothing.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="22_wget_checksum_mismatch"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"
install_fake_wget
hide_tools curl
FAKE_SHA_MODE=mismatch
export FAKE_SHA_MODE

run_install install

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "1" "a mismatch fails"
assert_contains "$INSTALL_OUTPUT" "checksum mismatch" "says so"
[ -e "$DEST" ] && _assert_fail "nothing is installed" || _assert_pass "nothing is installed"

install_report "$SCENARIO_NAME"
exit $?
