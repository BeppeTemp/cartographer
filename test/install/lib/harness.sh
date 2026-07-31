#!/bin/sh
# lib/harness.sh — network-free installer harness (D121 / WP3).
#
# Provides an isolated PATH, install directory, and a stateful fake
# "cartographer" binary so install.sh's update path can be exercised without
# touching the network or a real service. Sourced by each scenario script;
# expects REPO_ROOT and INSTALL_LIB_DIR to already be set.

# install_setup <scenario_name>
#   Prepares an isolated sandbox for one scenario: fake curl on PATH, an
#   install dir, and a fresh invocation log.
install_setup() {
    scenario_name="$1"
    SCENARIO_TMP=$(mktemp -d "${TMPDIR:-/tmp}/cartographer_install_test.${scenario_name}.XXXXXX")
    mkdir -p "${SCENARIO_TMP}/fakebin" "${SCENARIO_TMP}/install" "${SCENARIO_TMP}/asset"

    FAKE_BIN_LOG="${SCENARIO_TMP}/invocations.log"
    : > "$FAKE_BIN_LOG"

    NEW_BINARY="${SCENARIO_TMP}/asset/cartographer-new"
    CARTOGRAPHER_INSTALL_DIR="${SCENARIO_TMP}/install"
    DEST="${CARTOGRAPHER_INSTALL_DIR}/cartographer"

    cp "${INSTALL_LIB_DIR}/fake-curl.sh" "${SCENARIO_TMP}/fakebin/curl"
    chmod +x "${SCENARIO_TMP}/fakebin/curl"

    PATH="${SCENARIO_TMP}/fakebin:${PATH}"
    export PATH CARTOGRAPHER_INSTALL_DIR
    unset GITHUB_TOKEN
}

# write_fake_binary <path> <version>
#   Writes a stateful fake cartographer executable at <path>. It logs every
#   invocation (argv) to $FAKE_BIN_LOG, reports <version> for `version`, and
#   exits with $FAKE_REPAIR_EXIT (default 0) for `upgrade-repair`.
write_fake_binary() {
    bin_path="$1"
    bin_version="$2"
    cat > "$bin_path" <<FAKEBIN
#!/bin/sh
printf '%s\n' "\$*" >> "${FAKE_BIN_LOG}"
case "\$1" in
    version)
        printf '%s\n' "${bin_version}"
        ;;
    upgrade-repair)
        exit "\${FAKE_REPAIR_EXIT:-0}"
        ;;
    *)
        exit 0
        ;;
esac
FAKEBIN
    chmod +x "$bin_path"
}

# run_install <subcommand>
#   Runs install.sh <subcommand> against the scenario sandbox, capturing
#   combined stdout+stderr into $INSTALL_OUTPUT and the exit code into
#   $INSTALL_RC.
run_install() {
    INSTALL_OUTPUT_FILE="${SCENARIO_TMP}/output.log"
    FAKE_NEW_BINARY="$NEW_BINARY"
    export FAKE_NEW_BINARY FAKE_TAG FAKE_REPAIR_EXIT
    sh "${REPO_ROOT}/install.sh" "$1" >"$INSTALL_OUTPUT_FILE" 2>&1
    INSTALL_RC=$?
    INSTALL_OUTPUT=$(cat "$INSTALL_OUTPUT_FILE")
}

# install_report <scenario_name>
#   Prints the pass/fail summary and returns the suite exit code.
install_report() {
    echo ""
    if [ "$INSTALL_FAILURES" -eq 0 ]; then
        echo "[SCENARIO $1] PASS"
        return 0
    fi
    echo "[SCENARIO $1] FAIL (${INSTALL_FAILURES} assertion(s) failed)"
    return 1
}
