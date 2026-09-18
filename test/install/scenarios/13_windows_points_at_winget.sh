#!/bin/sh
# scenarios/13_windows_points_at_winget.sh — piped from a Windows shell
# environment (Git Bash, MSYS2, Cygwin), install.sh must refuse and name the one
# channel that works there, without downloading or writing anything (D218).
#
# The refusal itself is not new; what is asserted here is that it is *actionable*.
# A user who lands on "unsupported OS: mingw64_nt-10.0" has no way to know that
# `winget install BeppeTemp.Cartographer` is the answer, and winget being the only
# Windows channel means that string is the whole answer.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="13_windows_points_at_winget"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

# A stub `uname` ahead of the real one on PATH, reporting what Git Bash reports.
# The fake curl the harness installs stays in place: if install.sh got past the
# OS check it would call it, and the log below is what proves it did not.
cat > "${SCENARIO_TMP}/fakebin/uname" <<'STUB'
#!/bin/sh
case "$1" in
    -s) printf 'MINGW64_NT-10.0-22631\n' ;;
    -m) printf 'x86_64\n' ;;
    *)  printf 'MINGW64_NT-10.0-22631\n' ;;
esac
STUB
chmod +x "${SCENARIO_TMP}/fakebin/uname"

run_install install

echo "--- Assertions ---"
assert_eq "$INSTALL_RC" "1" "install.sh refuses on a Windows shell environment"
assert_contains "$INSTALL_OUTPUT" "winget install BeppeTemp.Cartographer" \
    "names the one supported Windows channel"
assert_not_contains "$INSTALL_OUTPUT" "unsupported OS" \
    "does not fall through to the generic refusal, which names no remedy"
# The Scheduled Task of D217 outlives `winget uninstall`, which runs no
# Cartographer code: the counterpart of install.sh's own uninstall refusal.
assert_contains "$INSTALL_OUTPUT" "cartographer service uninstall" \
    "warns that the service must be uninstalled by Cartographer itself"
# Nothing was downloaded: the asset request is the one thing the fake curl
# records, so an absent marker means it was never reached.
if [ -f "${SCENARIO_TMP}/last-asset" ]; then
    _assert_fail "install.sh requested an asset on an unsupported OS: $(cat "${SCENARIO_TMP}/last-asset")"
else
    _assert_pass "no asset is requested"
fi
if [ -f "$DEST" ]; then
    _assert_fail "install.sh wrote a binary to ${DEST} on an unsupported OS"
else
    _assert_pass "nothing is installed"
fi

install_report "$SCENARIO_NAME"
exit $?
