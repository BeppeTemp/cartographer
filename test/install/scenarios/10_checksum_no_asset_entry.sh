#!/bin/sh
# scenarios/10_checksum_no_asset_entry.sh (D192) — sha256sums.txt exists but
# covers some other asset. Before D192 install.sh proceeded silently; that is
# the shape a truncated or tampered manifest has, so it is now an error.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="10_checksum_no_asset_entry"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
FAKE_REPAIR_EXIT="0"
FAKE_SHA_MODE="noasset"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install install

echo "--- Assertions ---"
if [ "$INSTALL_RC" -eq 0 ]; then
    _assert_fail "a checksum file that does not cover the asset must fail the install (rc=0)"
else
    _assert_pass "a checksum file that does not cover the asset fails the install"
fi
assert_file_contains "$INSTALL_OUTPUT_FILE" "no entry for" "the failure says what is missing"

install_report "$SCENARIO_NAME"
exit $?
