#!/bin/sh
# scenarios/11_checksum_absent.sh (D192, D1022) — a manifest that cannot be
# fetched is an error: every release since v0.1.0 ships sha256sums.txt, so its
# absence is a broken release or a request dropped to skip verification, never
# an old tag.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="11_checksum_absent"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
FAKE_REPAIR_EXIT="0"
FAKE_SHA_MODE="absent"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install install

echo "--- Assertions ---"
if [ "$INSTALL_RC" -eq 0 ]; then
    _assert_fail "a release whose checksum file cannot be fetched must fail the install (rc=0)"
else
    _assert_pass "a release whose checksum file cannot be fetched fails the install"
fi
assert_file_contains "$INSTALL_OUTPUT_FILE" "cannot download sha256sums.txt" "the failure names the missing manifest"
if [ -e "$DEST" ]; then
    _assert_fail "nothing unverified may be installed: ${DEST} exists"
else
    _assert_pass "no binary is installed"
fi

install_report "$SCENARIO_NAME"
exit $?
