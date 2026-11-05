#!/bin/sh
# scenarios/25_fetcher_flags.sh — both fetchers are limited to https, and the
# token is never sent through wget.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="25_fetcher_flags"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

GITHUB_TOKEN=token-a
export GITHUB_TOKEN
run_install install
echo "--- Assertions (curl) ---"
assert_eq "$INSTALL_RC" "0" "installs through curl"
assert_file_contains "${SCENARIO_TMP}/curl-argv" "--proto =https --proto-redir =https" "curl is limited to https, redirects included"
assert_file_contains "${SCENARIO_TMP}/curl-argv" "Authorization: Bearer token-a" "curl sends the token"

rm -f "$DEST" "${SCENARIO_TMP}/curl-argv"
install_fake_wget
hide_tools curl
run_install install
echo "--- Assertions (wget) ---"
assert_eq "$INSTALL_RC" "0" "installs through wget"
assert_file_contains "${SCENARIO_TMP}/wget-argv" "--https-only" "wget gets --https-only"
assert_file_not_contains "${SCENARIO_TMP}/wget-argv" "Authorization" "wget never carries the token"
assert_file_not_contains "${SCENARIO_TMP}/wget-argv" "token-a" "the token is not on wget's command line"
assert_contains "$INSTALL_OUTPUT" "GITHUB_TOKEN is not used with wget" "says the token is not used"

rm -f "$DEST" "${SCENARIO_TMP}/wget-argv"
FAKE_WGET_BUSYBOX=1
export FAKE_WGET_BUSYBOX
run_install install
unset FAKE_WGET_BUSYBOX
echo "--- Assertions (busybox wget) ---"
assert_eq "$INSTALL_RC" "0" "installs through a wget without --https-only"
assert_file_not_contains "${SCENARIO_TMP}/wget-argv" "--https-only" "no flag the wget does not know"

unset GITHUB_TOKEN
install_report "$SCENARIO_NAME"
exit $?
