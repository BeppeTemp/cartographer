#!/bin/sh
# scenarios/23_no_fetcher_no_hash_tool.sh — neither curl nor wget fails first; no hash tool refuses to install.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="23_no_fetcher_no_hash_tool"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"
hide_tools curl wget

run_install install

echo "--- Assertions (no fetcher) ---"
assert_eq "$INSTALL_RC" "1" "fails with no curl and no wget"
assert_contains "$INSTALL_OUTPUT" "need curl or wget" "names the requirement"

install_fake_wget
hide_tools curl sha256sum shasum openssl
run_install install

echo "--- Assertions (no hash tool) ---"
assert_eq "$INSTALL_RC" "1" "fails with no sha256 tool"
assert_contains "$INSTALL_OUTPUT" "no sha256 tool: refusing to install unverified" "never installs unverified"
[ -e "$DEST" ] && _assert_fail "nothing is installed" || _assert_pass "nothing is installed"

install_report "$SCENARIO_NAME"
exit $?
