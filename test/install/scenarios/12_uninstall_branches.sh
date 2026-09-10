#!/bin/sh
# scenarios/12_uninstall_branches.sh (D192) — `uninstall` used to remove the
# binary and nothing else, leaving launchd/systemd units pointing at a missing
# executable. It now detects them, names the commands that remove them, and
# exits non-zero unless --binary-only says the operator meant it.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"
# shellcheck source=../lib/harness.sh
. "${INSTALL_LIB_DIR}/harness.sh"

SCENARIO_NAME="12_uninstall_branches"
echo "=== Scenario ${SCENARIO_NAME} ==="

install_setup "$SCENARIO_NAME"
trap 'rm -rf "$SCENARIO_TMP"' EXIT

FAKE_TAG="v2.0.0"
FAKE_REPAIR_EXIT="0"

# An isolated HOME: unit detection reads $HOME, and the real machine's units
# must not decide the outcome of this test.
HOME="${SCENARIO_TMP}/home"
export HOME
mkdir -p "${HOME}/Library/LaunchAgents" "${HOME}/.config/systemd/user"

echo "--- Branch 1: nothing installed ---"
run_install uninstall
assert_eq "$INSTALL_RC" "0" "uninstall with nothing installed succeeds"
assert_file_contains "$INSTALL_OUTPUT_FILE" "nothing to do" "and says so"

echo "--- Branch 2: a binary, no units ---"
write_fake_binary "$DEST" "$FAKE_TAG"
run_install uninstall
assert_eq "$INSTALL_RC" "0" "uninstall with no units behaves exactly as before"
if [ -e "$DEST" ]; then
    _assert_fail "the binary should have been removed"
else
    _assert_pass "the binary is removed"
fi

echo "--- Branch 3: a service is present, so uninstall refuses ---"
write_fake_binary "$DEST" "$FAKE_TAG"
touch "${HOME}/.config/systemd/user/cartographer.service"
run_install uninstall
if [ "$INSTALL_RC" -eq 0 ]; then
    _assert_fail "uninstall must not silently leave a unit pointing at a missing binary (rc=0)"
else
    _assert_pass "uninstall refuses while a unit is installed"
fi
assert_file_contains "$INSTALL_OUTPUT_FILE" "cartographer.service" "the refusal names the unit it found"
assert_file_contains "$INSTALL_OUTPUT_FILE" "service uninstall" "and the command that removes it"
assert_file_contains "$INSTALL_OUTPUT_FILE" "uninstall --binary-only" "and the explicit way to proceed anyway"
assert_executable "$DEST" "the binary is still there: the refusal changed nothing"

echo "--- Branch 4: --binary-only proceeds ---"
run_install uninstall --binary-only
assert_eq "$INSTALL_RC" "0" "--binary-only removes the binary anyway"
if [ -e "$DEST" ]; then
    _assert_fail "--binary-only should have removed the binary"
else
    _assert_pass "--binary-only removes the binary"
fi
assert_file_contains "$INSTALL_OUTPUT_FILE" "binary only" "and states what it left behind"

echo "--- Branch 5: a partial state (timer without service) is detected too ---"
rm -f "${HOME}/.config/systemd/user/cartographer.service"
write_fake_binary "$DEST" "$FAKE_TAG"
touch "${HOME}/.config/systemd/user/cartographer-sync.timer"
run_install uninstall
if [ "$INSTALL_RC" -eq 0 ]; then
    _assert_fail "a timer without a service is still a leftover unit (rc=0)"
else
    _assert_pass "a partial installation is detected"
fi
assert_file_contains "$INSTALL_OUTPUT_FILE" "cartographer-sync.timer" "the refusal names the timer"

install_report "$SCENARIO_NAME"
exit $?
