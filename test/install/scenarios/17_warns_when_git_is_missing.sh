#!/bin/sh
# scenarios/17_warns_when_git_is_missing.sh — a missing git is a warning at the
# end of a successful install, never a failure (#1467): a KB is a git
# repository and `cartographer setup` stops without it. The hint is the one for
# the OS: xcode-select on macOS, the package manager elsewhere.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="17_warns_when_git_is_missing"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
FAKE_REPAIR_EXIT="0"
write_fake_binary "$NEW_BINARY" "$FAKE_TAG"

# A PATH that holds the system tools but no git: every entry of /bin and
# /usr/bin symlinked except git.
TOOLS="${SCENARIO_TMP}/tools"
mkdir -p "$TOOLS"
for d in /bin /usr/bin; do
    for f in "$d"/*; do
        n=$(basename "$f")
        [ "$n" = git ] && continue
        [ -e "${TOOLS}/${n}" ] || ln -s "$f" "${TOOLS}/${n}"
    done
done
FULL_PATH="$PATH"

# uname stub: the OS the hint is chosen on.
write_uname() {
    cat > "${SCENARIO_TMP}/fakebin/uname" <<STUB
#!/bin/sh
case "\$1" in
    -m) printf 'x86_64\n' ;;
    *)  printf '%s\n' "$1" ;;
esac
STUB
    chmod +x "${SCENARIO_TMP}/fakebin/uname"
}

# No Command Line Tools: xcode-select -p fails (the host's own would answer).
cat > "${SCENARIO_TMP}/fakebin/xcode-select" <<'STUB'
#!/bin/sh
exit 2
STUB
chmod +x "${SCENARIO_TMP}/fakebin/xcode-select"
PATH="${SCENARIO_TMP}/fakebin:${TOOLS}"
write_uname Darwin
run_install install
echo "--- Assertions (macOS, no git) ---"
assert_eq "$INSTALL_RC" "0" "a missing git does not fail the install"
assert_executable "$DEST" "the binary is installed"
assert_contains "$INSTALL_OUTPUT" "warning: git is not installed" "warns about git"
assert_contains "$INSTALL_OUTPUT" "xcode-select --install" "macOS gets the Command Line Tools hint"

rm -f "$DEST"
write_uname Linux
run_install install
echo "--- Assertions (Linux, no git) ---"
assert_eq "$INSTALL_RC" "0" "a missing git does not fail the install on Linux"
assert_contains "$INSTALL_OUTPUT" "package manager" "Linux gets the generic hint"
assert_not_contains "$INSTALL_OUTPUT" "xcode-select" "Linux is not told about xcode-select"

# With a git on the PATH the warning is silent.
printf '#!/bin/sh\nexit 0\n' > "${SCENARIO_TMP}/fakebin/git"
chmod +x "${SCENARIO_TMP}/fakebin/git"
rm -f "$DEST"
run_install install
echo "--- Assertions (git present) ---"
assert_eq "$INSTALL_RC" "0" "install succeeds with git present"
assert_not_contains "$INSTALL_OUTPUT" "git is not installed" "no warning when git is on the PATH"

# macOS without the Command Line Tools: /usr/bin/git is a shim, so git is on the
# PATH, but xcode-select -p fails. The warning must still fire.
write_uname Darwin
rm -f "$DEST"
run_install install
echo "--- Assertions (macOS, git shim, no Command Line Tools) ---"
assert_eq "$INSTALL_RC" "0" "install succeeds"
assert_contains "$INSTALL_OUTPUT" "warning: git is not installed" "warns although git is on the PATH"
assert_contains "$INSTALL_OUTPUT" "xcode-select --install" "names the Command Line Tools install"

printf '#!/bin/sh\nexit 0\n' > "${SCENARIO_TMP}/fakebin/xcode-select"
rm -f "$DEST"
run_install install
echo "--- Assertions (macOS, Command Line Tools present) ---"
assert_not_contains "$INSTALL_OUTPUT" "git is not installed" "no warning when the tools are installed"

# A rerun that finds the current version installed still warns: that is when a
# user without git tries again.
rm -f "${SCENARIO_TMP}/fakebin/git"
printf '#!/bin/sh\nexit 2\n' > "${SCENARIO_TMP}/fakebin/xcode-select"
run_install install
echo "--- Assertions (already installed, no usable git) ---"
assert_contains "$INSTALL_OUTPUT" "already installed" "the second run is the early return"
assert_contains "$INSTALL_OUTPUT" "warning: git is not installed" "the early return warns too"

PATH="$FULL_PATH"
install_report "$SCENARIO_NAME"
exit $?
