#!/bin/sh
# goreleaser_guard.sh — static guard for .goreleaser.yaml's generated Cask
# postflight hook (D121 / WP3). It tests the repository template checked
# into this repo — the only repository-side Cask source of truth — not the
# file GoReleaser publishes to BeppeTemp/homebrew-tap.

GUARD_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "${GUARD_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${GUARD_DIR}/lib"

# shellcheck source=lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"

GORELEASER_FILE="${REPO_ROOT}/.goreleaser.yaml"

echo "=== Guard: .goreleaser.yaml Cask postflight ==="

if [ ! -f "$GORELEASER_FILE" ]; then
    _assert_fail "goreleaser template not found: ${GORELEASER_FILE}"
else
    assert_file_contains "$GORELEASER_FILE" 'com.apple.quarantine' \
        "keeps the macOS quarantine removal"
    assert_file_contains "$GORELEASER_FILE" '#{HOMEBREW_PREFIX}/bin/cartographer' \
        "invokes the stable linked binary, never a versioned Caskroom path"
    assert_file_contains "$GORELEASER_FILE" 'upgrade-repair' \
        "invokes upgrade-repair"
    assert_file_contains "$GORELEASER_FILE" 'must_succeed: false' \
        "runs upgrade-repair with non-fatal system_command semantics"

    # The upgrade-repair system_command spans two lines (path, then args):
    # check the path line immediately preceding `args: ["upgrade-repair"]`
    # rather than the whole file, so an unrelated mention of "Caskroom" (e.g.
    # in a comment) does not produce a false failure.
    args_line=$(grep -n 'args: \["upgrade-repair"\]' "$GORELEASER_FILE" | head -1 | cut -d: -f1)
    if [ -n "$args_line" ]; then
        command_line=$(sed -n "$((args_line - 1))p" "$GORELEASER_FILE")
        case "$command_line" in
            *'#{HOMEBREW_PREFIX}/bin/cartographer'*)
                _assert_pass "upgrade-repair runs the stable linked binary, not a versioned Caskroom path"
                ;;
            *)
                _assert_fail "upgrade-repair invocation does not use the stable linked binary: ${command_line}"
                ;;
        esac
    else
        _assert_fail "could not locate the upgrade-repair system_command invocation"
    fi
fi

echo ""
if [ "$INSTALL_FAILURES" -eq 0 ]; then
    echo "[GUARD] PASS"
    exit 0
fi
echo "[GUARD] FAIL (${INSTALL_FAILURES} assertion(s) failed)"
exit 1
