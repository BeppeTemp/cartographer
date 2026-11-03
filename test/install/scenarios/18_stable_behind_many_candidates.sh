#!/bin/sh
# scenarios/18_stable_behind_many_candidates.sh — twelve release candidates listed before the newest stable tag (they stay until their version ships, D1384): the stable tag is still found.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="18_stable_behind_many_candidates"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
FAKE_RC_TAGS=""
for n in 12 11 10 09 08 07 06 05 04 03 02 01; do FAKE_RC_TAGS="${FAKE_RC_TAGS} v2.1.0-rc.202611${n}"; done
FAKE_REPAIR_EXIT="0"
FAKE_SHA_MODE="valid"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install install

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "0" "the install succeeds"
assert_file_contains "$INSTALL_OUTPUT_FILE" "installing cartographer v2.0.0" "the stable tag behind twelve candidates is chosen"
assert_file_contains "${SCENARIO_TMP}/release-list-calls" "per_page=100" "the list is read with a window of 100"

install_report "$SCENARIO_NAME"
exit $?
