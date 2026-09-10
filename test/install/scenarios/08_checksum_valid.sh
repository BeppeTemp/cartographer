#!/bin/sh
# scenarios/08_checksum_valid.sh (D192) — the release ships a sha256sums.txt
# that covers the asset with the right digest: the install proceeds and says so.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="08_checksum_valid"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
FAKE_REPAIR_EXIT="0"
FAKE_SHA_MODE="valid"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install install

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "0" "a matching checksum installs"
assert_executable "$DEST" "the binary is installed"
assert_file_contains "$INSTALL_OUTPUT_FILE" "checksum OK" "the verification is reported, not silent"

install_report "$SCENARIO_NAME"
exit $?
