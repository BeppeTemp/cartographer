#!/bin/sh
# scenarios/09_checksum_mismatch.sh (D192) — a digest that does not match the
# downloaded asset must fail the install, and must not leave a binary behind.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="09_checksum_mismatch"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
FAKE_REPAIR_EXIT="0"
FAKE_SHA_MODE="mismatch"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install install

echo "--- Assertions ---"
if [ "$INSTALL_RC" -eq 0 ]; then
    _assert_fail "a checksum mismatch must fail the install (rc=0)"
else
    _assert_pass "a checksum mismatch fails the install"
fi
assert_file_contains "$INSTALL_OUTPUT_FILE" "checksum mismatch" "the failure names the reason"
if [ -e "$DEST" ]; then
    _assert_fail "a tampered asset must not be installed: ${DEST} exists"
else
    _assert_pass "no binary is left behind"
fi

install_report "$SCENARIO_NAME"
exit $?
