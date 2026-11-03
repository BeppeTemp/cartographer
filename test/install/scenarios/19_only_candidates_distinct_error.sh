#!/bin/sh
# scenarios/19_only_candidates_distinct_error.sh — the release list holds candidates only: the error says no stable release was found, not that the API is rate-limited.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="19_only_candidates_distinct_error"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
FAKE_RC_TAGS="v2.1.0-rc.20261112 v2.1.0-rc.20261111"
FAKE_NO_STABLE=1
FAKE_REPAIR_EXIT="0"
FAKE_SHA_MODE="valid"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install install

echo "--- Assertions ---"
if [ "$INSTALL_RC" -eq 0 ]; then
    _assert_fail "no stable release must fail the install (rc=0)"
else
    _assert_pass "no stable release fails the install"
fi
assert_file_contains "$INSTALL_OUTPUT_FILE" "no stable release among the newest 100" "the message names the cause"
assert_file_not_contains "$INSTALL_OUTPUT_FILE" "rate-limited" "it does not blame rate limiting"
if [ -e "$DEST" ]; then
    _assert_fail "nothing may be installed: ${DEST} exists"
else
    _assert_pass "no binary is installed"
fi

install_report "$SCENARIO_NAME"
exit $?
