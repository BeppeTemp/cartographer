#!/bin/sh
# scenarios/24_arm_targets.sh — 32-bit ARM maps to the linux-arm asset; ARMv6 is refused.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="24_arm_targets"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"
fake_uname Linux armv7l
run_install install
echo "--- Assertions (armv7l) ---"
assert_eq "$INSTALL_RC" "0" "armv7l installs"
assert_eq "$(cat "${SCENARIO_TMP}/last-asset")" "cartographer-linux-arm" "armv7l downloads cartographer-linux-arm"

rm -f "$DEST"
fake_uname Linux aarch64 32
run_install install
echo "--- Assertions (aarch64 kernel, 32-bit userland) ---"
assert_eq "$(cat "${SCENARIO_TMP}/last-asset")" "cartographer-linux-arm" "a 32-bit userland gets the arm asset"

rm -f "$DEST"
fake_uname Linux aarch64 64
run_install install
echo "--- Assertions (aarch64) ---"
assert_eq "$(cat "${SCENARIO_TMP}/last-asset")" "cartographer-linux-arm64" "a 64-bit userland keeps arm64"

fake_uname Linux armv6l
run_install install
echo "--- Assertions (armv6l) ---"
assert_eq "$INSTALL_RC" "1" "armv6l is refused"
assert_contains "$INSTALL_OUTPUT" "needs ARMv7" "says why"

install_report "$SCENARIO_NAME"
exit $?
