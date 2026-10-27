#!/bin/sh
# scenarios/16_install_tag_pins_a_candidate.sh (D1192) — CARTOGRAPHER_INSTALL_TAG installs that tag (a
# release candidate) without reading the release list, verified against its own
# sha256sums.txt exactly like a release: no manifest, no install; a value that
# is not a release tag is refused before any download.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="16_install_tag_pins_a_candidate"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT


FAKE_TAG="v2.0.0"
FAKE_RC_TAGS="v2.1.0-rc.20261111"
FAKE_REPAIR_EXIT="0"
FAKE_SHA_MODE="valid"
write_fake_binary "$NEW_BINARY" "v2.1.0-rc.20261111"

CARTOGRAPHER_INSTALL_TAG="v2.1.0-rc.20261111" run_install install

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "0" "the pinned candidate installs"
assert_file_contains "$INSTALL_OUTPUT_FILE" "installing cartographer v2.1.0-rc.20261111" "the pinned tag is the one installed"
assert_file_contains "$INSTALL_OUTPUT_FILE" "checksum OK" "the candidate is verified like a release"
assert_file_contains "${SCENARIO_TMP}/last-asset-url" "/v2.1.0-rc.20261111/" "the candidate's asset is downloaded"
if [ -e "${SCENARIO_TMP}/release-list-calls" ]; then
    _assert_fail "a pinned tag must not read the release list"
else
    _assert_pass "the release list is not read"
fi

# Back to stable: the same installer without the variable takes the newest release.
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"
run_install update
assert_eq "$INSTALL_RC" "0" "update without the variable succeeds"
assert_file_contains "$INSTALL_OUTPUT_FILE" "updating cartographer v2.1.0-rc.20261111 -> v2.0.0" "update goes back to the newest stable release"

rm -f "$DEST"
FAKE_SHA_MODE="absent"
CARTOGRAPHER_INSTALL_TAG="v2.1.0-rc.20261111" run_install install
if [ "$INSTALL_RC" -eq 0 ]; then
    _assert_fail "a pinned tag without sha256sums.txt must fail the install"
else
    _assert_pass "a pinned tag without sha256sums.txt fails the install"
fi
assert_file_contains "$INSTALL_OUTPUT_FILE" "cannot download sha256sums.txt" "the failure names the missing manifest"
if [ -e "$DEST" ]; then
    _assert_fail "nothing unverified may be installed: ${DEST} exists"
else
    _assert_pass "no binary is installed"
fi

FAKE_SHA_MODE="valid"
CARTOGRAPHER_INSTALL_TAG='v2.1.0/../../evil' run_install install
assert_file_contains "$INSTALL_OUTPUT_FILE" "is not a release tag" "a value that is not a release tag is refused"
if [ -e "$DEST" ]; then
    _assert_fail "a refused tag installs nothing"
else
    _assert_pass "a refused tag installs nothing"
fi

install_report "$SCENARIO_NAME"
exit $?
