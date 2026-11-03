#!/bin/sh
# scenarios/20_no_temp_files_left.sh — a successful install leaves no checksum file and no temp directory, and the temp directory was created next to the destination (a rename, never a cross-filesystem copy).

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="20_no_temp_files_left"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

REAL_MKTEMP=$(command -v mktemp)
cat > "${SCENARIO_TMP}/fakebin/mktemp" <<EOS
#!/bin/sh
echo "\$*" >> "${SCENARIO_TMP}/mktemp-calls"
exec "${REAL_MKTEMP}" "\$@"
EOS
chmod +x "${SCENARIO_TMP}/fakebin/mktemp"

FAKE_TAG="v2.0.0"
FAKE_REPAIR_EXIT="0"
FAKE_SHA_MODE="valid"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

run_install install

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "0" "the install succeeds"
assert_executable "$DEST" "the binary is installed"
assert_file_contains "${SCENARIO_TMP}/mktemp-calls" "${CARTOGRAPHER_INSTALL_DIR}/.cartographer-install." "the temp directory is created in the destination directory"
leftovers=$(find "$CARTOGRAPHER_INSTALL_DIR" -mindepth 1 ! -name cartographer 2>/dev/null)
assert_eq "$leftovers" "" "nothing but the binary is left in the install dir"
shas=$(find "${TMPDIR:-/tmp}" -maxdepth 1 -name 'tmp.*.sha' -newer "${SCENARIO_TMP}/mktemp-calls" 2>/dev/null)
assert_eq "$shas" "" "no *.sha file is left in the temp dir"

install_report "$SCENARIO_NAME"
exit $?
