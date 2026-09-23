#!/bin/sh
# goreleaser_guard.sh — static guard for .goreleaser.yaml's packaging blocks:
# the generated Cask install steps (D121, D199) and the Windows/winget shape
# (D218). It tests the repository template checked into this repo — the only
# repository-side source of truth for both — not the files GoReleaser publishes to
# BeppeTemp/homebrew-tap and BeppeTemp/winget-pkgs.
#
# Everything asserted here is something whose breakage is either silent or only
# visible on a real Windows machine: an archive format that turns the winget
# command into `cartographer.exe`, a submission quietly retargeted away from the
# community repository, or a Pro-only field that reads as configuration and is
# ignored.

GUARD_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "${GUARD_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${GUARD_DIR}/lib"

# shellcheck source=lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"

GORELEASER_FILE="${REPO_ROOT}/.goreleaser.yaml"

echo "=== Guard: .goreleaser.yaml packaging (Cask + Windows/winget) ==="

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

    # --- Windows targets and the winget channel (D218) ---------------------
    assert_file_contains "$CODE_FILE" '      - windows' \
        "builds for windows"
    assert_file_contains "$CODE_FILE" 'format_overrides:' \
        "overrides the archive format per platform"
    # The zip is what gives winget a portable nested installer with a clean
    # `cartographer` alias; a raw binary would register `cartographer.exe` as the
    # command name, and mixing the two for one platform is refused outright by the
    # pipe (errMixedFormats).
    if grep -A2 'format_overrides:' "$CODE_FILE" | grep -q 'goos: windows' \
        && grep -A3 'format_overrides:' "$CODE_FILE" | grep -q 'formats: \[zip\]'; then
        _assert_pass "the windows archive is a zip"
    else
        _assert_fail "the windows archive is a zip — no 'goos: windows' override with 'formats: [zip]' found"
    fi
    if grep -A3 'format_overrides:' "$CODE_FILE" | grep -q 'formats: \[binary\]'; then
        _assert_fail "no windows override produces a raw binary (would give InstallerType portable and a cartographer.exe command)"
    else
        _assert_pass "no windows override produces a raw binary"
    fi

    assert_file_contains "$CODE_FILE" 'package_identifier: BeppeTemp.Cartographer' \
        "publishes the agreed winget package identifier"
    assert_file_contains "$CODE_FILE" 'name: winget-pkgs' \
        "pushes the manifests to a winget-pkgs fork"
    # A submission retargeted at a private manifest repository still succeeds, and
    # silently stops being installable with `winget install BeppeTemp.Cartographer`.
    if grep -A4 'pull_request:' "$CODE_FILE" | grep -q 'owner: microsoft'; then
        _assert_pass "the pull request targets microsoft/winget-pkgs"
    else
        _assert_fail "the pull request targets microsoft/winget-pkgs — base owner is not microsoft"
    fi
    assert_file_not_contains "$CODE_FILE" 'use:' \
        "sets no 'use:' (GoReleaser Pro field, silently ignored in OSS)"
fi

# --- The embedded Atlas UI (D227) -------------------------------------------
# Release archives, the Cask, the winget zip and the container all carry the
# output of `go build ./cmd/cartographer`, and the UI reaches them only through
# go:embed of the committed bundle. Three ways to lose it silently: a release
# build that depends on a frontend toolchain, a bundle git ignores (GoReleaser
# builds from a clean checkout), and a Docker build context that excludes it.
# Scenario 14 builds the binary and serves the UI from it; this guards the
# packaging inputs that scenario cannot see.
echo ""
echo "=== Guard: the embedded Atlas UI reaches every package (D227) ==="
if [ -f "$GORELEASER_FILE" ]; then
    assert_file_contains "$CODE_FILE" 'main: ./cmd/cartographer' \
        "release builds compile the one package that embeds the UI"
    assert_file_not_contains "$CODE_FILE" 'npm' \
        "the release pipeline runs no npm: the bundle is committed, Node is not a release dependency"
fi
if [ -n "$(git -C "$REPO_ROOT" ls-files -- internal/webui/dist/index.html internal/webui/dist/provenance.json)" ]; then
    _assert_pass "the UI bundle is tracked by git"
else
    _assert_fail "internal/webui/dist is not tracked by git: release archives would ship without the UI"
fi
if git -C "$REPO_ROOT" check-ignore -q internal/webui/dist/index.html; then
    _assert_fail "internal/webui/dist/index.html is git-ignored (a 'dist/' rule without its '!internal/webui/dist/' exception)"
else
    _assert_pass "the UI bundle is not git-ignored"
fi
assert_file_contains "${REPO_ROOT}/Dockerfile" 'COPY . .' \
    "the container build copies the whole tree, bundle included"
if [ -f "${REPO_ROOT}/.dockerignore" ] && grep -v '^[[:space:]]*#' "${REPO_ROOT}/.dockerignore" | grep -Eq '(^|/)(internal|webui|dist)(/|$)|^\*'; then
    _assert_fail ".dockerignore excludes the embedded bundle (internal/webui/dist) from the container build"
else
    _assert_pass ".dockerignore leaves the embedded bundle in the container build context"
fi

echo ""
if [ "$INSTALL_FAILURES" -eq 0 ]; then
    echo "[GUARD] PASS"
    exit 0
fi
echo "[GUARD] FAIL (${INSTALL_FAILURES} assertion(s) failed)"
exit 1
