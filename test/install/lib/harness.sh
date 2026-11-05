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
    # hide_tools sets INSTALL_PATH: what install.sh sees when the harness keeps its own PATH.
    INSTALL_PATH=""
    unset GITHUB_TOKEN
}

# hide_tools <tool>...
#   Narrows the PATH install.sh sees to fakebin plus a directory holding only the tools
#   install.sh needs, minus the ones named, so a scenario can run on a host with
#   no curl or no hash tool. `busybox` is never linked, so that hash fallback is
#   absent too. Call it after the fakes are installed.
hide_tools() {
    rm -rf "${SCENARIO_TMP}/minbin"; mkdir -p "${SCENARIO_TMP}/minbin"
    for h in "$@"; do [ "$h" = curl ] && rm -f "${SCENARIO_TMP}/fakebin/curl"; done
    for t in sh uname tr mktemp sed grep head cut chmod mv rm cp cat mkdir basename dirname getconf sha256sum shasum openssl; do
        skip=0
        for h in "$@"; do [ "$h" = "$t" ] && skip=1; done
        [ "$skip" = 1 ] && continue
        p=$(command -v "$t" 2>/dev/null) || continue
        case "$p" in /*) ln -sf "$p" "${SCENARIO_TMP}/minbin/$t" ;; esac
    done
    INSTALL_PATH="${SCENARIO_TMP}/fakebin:${SCENARIO_TMP}/minbin"
}

# install_fake_wget: a fake wget in fakebin that maps GNU/busybox flags onto
# the fake curl (-q and --header ignored, -O FILE -> -o FILE).
install_fake_wget() {
    cp "${INSTALL_LIB_DIR}/fake-wget.sh" "${SCENARIO_TMP}/fakebin/wget"
    cp "${INSTALL_LIB_DIR}/fake-curl.sh" "${SCENARIO_TMP}/fakebin/fake-curl-impl"
    chmod +x "${SCENARIO_TMP}/fakebin/wget" "${SCENARIO_TMP}/fakebin/fake-curl-impl"
}

# fake_uname <os> <machine> [long_bit]
#   uname -s / -m answer as given and getconf LONG_BIT answers long_bit (64 when
#   omitted); everything else falls through to the real tools.
fake_uname() {
    real_uname=$(command -v uname)
    real_getconf=$(command -v getconf || echo /bin/false)
    {
        echo '#!/bin/sh'
        echo 'case "$1" in'
        echo "    -s) echo $1 ;;"
        echo "    -m) echo $2 ;;"
        echo "    *) exec $real_uname \"\$@\" ;;"
        echo 'esac'
    } > "${SCENARIO_TMP}/fakebin/uname"
    {
        echo '#!/bin/sh'
        echo "[ \"\$1\" = LONG_BIT ] && { echo ${3:-64}; exit 0; }"
        echo "exec $real_getconf \"\$@\""
    } > "${SCENARIO_TMP}/fakebin/getconf"
    chmod +x "${SCENARIO_TMP}/fakebin/uname" "${SCENARIO_TMP}/fakebin/getconf"
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
#   Never prefix it with an assignment (`VAR=x run_install ...`): in bash's POSIX
#   mode (macOS /bin/sh) the assignment outlives the function call, in dash it
#   does not. Export the variable, call, then unset it; run.sh refuses a
#   scenario that prefixes it.
run_install() {
    INSTALL_OUTPUT_FILE="${SCENARIO_TMP}/output.log"
    FAKE_NEW_BINARY="$NEW_BINARY"
    export FAKE_NEW_BINARY FAKE_TAG FAKE_REPAIR_EXIT SCENARIO_TMP
    export FAKE_RC_TAGS="${FAKE_RC_TAGS:-}"
    export FAKE_NO_STABLE="${FAKE_NO_STABLE:-}"
    export FAKE_SHA_MODE="${FAKE_SHA_MODE:-valid}"
    # Computed here, with the full PATH: a scenario may hide every hash tool.
    FAKE_DIGEST=$( (sha256sum "$NEW_BINARY" 2>/dev/null || shasum -a 256 "$NEW_BINARY") | cut -d' ' -f1)
    export FAKE_DIGEST
    env PATH="${INSTALL_PATH:-$PATH}" sh "${REPO_ROOT}/install.sh" "$@" >"$INSTALL_OUTPUT_FILE" 2>&1
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
