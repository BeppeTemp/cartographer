#!/bin/sh
# scenarios/21_wget_download_and_checksum.sh — with curl hidden and a fake wget the install downloads and verifies (also with GITHUB_TOKEN).

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="21_wget_download_and_checksum"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"
install_fake_wget
hide_tools curl

run_install install

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "0" "installs through wget"
assert_executable "$DEST" "the binary is installed"
assert_contains "$INSTALL_OUTPUT" "checksum OK" "the checksum is verified"

rm -f "$DEST"
GITHUB_TOKEN=token-a
export GITHUB_TOKEN
run_install install
unset GITHUB_TOKEN
echo "--- Assertions (token) ---"
assert_eq "$INSTALL_RC" "0" "installs through wget with GITHUB_TOKEN set"

install_report "$SCENARIO_NAME"
exit $?
