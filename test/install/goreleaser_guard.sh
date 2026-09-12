#!/bin/sh
# goreleaser_guard.sh — static guard for .goreleaser.yaml's generated Cask
# install steps (D121, D199). It tests the repository template checked into
# this repo — the only repository-side Cask source of truth — not the file
# GoReleaser publishes to BeppeTemp/homebrew-tap.

GUARD_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "${GUARD_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${GUARD_DIR}/lib"

# shellcheck source=lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"

GORELEASER_FILE="${REPO_ROOT}/.goreleaser.yaml"

echo "=== Guard: .goreleaser.yaml Cask postflight_steps ==="

if [ ! -f "$GORELEASER_FILE" ]; then
    _assert_fail "goreleaser template not found: ${GORELEASER_FILE}"
else
    # Comments legitimately name what the template no longer does; only the
    # YAML itself is checked.
    CODE_FILE=$(mktemp)
    trap 'rm -f "$CODE_FILE"' EXIT
    grep -v '^[[:space:]]*#' "$GORELEASER_FILE" > "$CODE_FILE"

    assert_file_contains "$CODE_FILE" 'postflight_steps do' \
        "declares the Cask install steps as postflight_steps"
    assert_file_contains "$CODE_FILE" 'com.apple.quarantine' \
        "keeps the macOS quarantine removal"
    assert_file_contains "$CODE_FILE" 'staged_path' \
        "removes quarantine from the staged path"
    assert_file_not_contains "$CODE_FILE" 'hooks:' \
        "sets no GoReleaser hooks (rendered as the deprecated postflight block)"
    assert_file_not_contains "$CODE_FILE" 'postflight do' \
        "writes no deprecated postflight block"
    assert_file_not_contains "$CODE_FILE" 'upgrade-repair' \
        "does not run upgrade-repair inside Homebrew's sandbox (the next sync repairs, D199)"
fi

echo ""
if [ "$INSTALL_FAILURES" -eq 0 ]; then
    echo "[GUARD] PASS"
    exit 0
fi
echo "[GUARD] FAIL (${INSTALL_FAILURES} assertion(s) failed)"
exit 1
