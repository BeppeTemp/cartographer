#!/bin/sh
# scenarios/15_skips_release_candidates.sh (D1192) — a release candidate listed first
# (a tag with a semver pre-release suffix) is never the default install: the
# newest tag without a suffix is, whatever GitHub's prerelease flag says.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="15_skips_release_candidates"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT


FAKE_TAG="v2.0.0"
FAKE_RC_TAGS="v2.1.0-rc.20261112 v2.1.0-rc.20261111"
FAKE_REPAIR_EXIT="0"
FAKE_SHA_MODE="valid"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install install

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "0" "the install succeeds"
assert_file_contains "$INSTALL_OUTPUT_FILE" "installing cartographer v2.0.0" "the stable tag is chosen, not the candidate listed first"
assert_file_contains "${SCENARIO_TMP}/last-asset-url" "/v2.0.0/" "the stable release's asset is downloaded"

install_report "$SCENARIO_NAME"
exit $?
