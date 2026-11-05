#!/bin/sh
# Cartographer client installer — install / update / uninstall.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- update
#   curl -fsSL .../install.sh | sh -s -- uninstall [--binary-only]
#
# On macOS, Homebrew is the preferred install method:
#   brew install beppetemp/tap/cartographer
#
# Needs curl or wget, and a SHA-256 tool (sha256sum, shasum, openssl or busybox).
# Environment:
#   CARTOGRAPHER_INSTALL_DIR  target directory (default: /usr/local/bin, falls
#                             back to ~/.local/bin when not writable)
#   CARTOGRAPHER_INSTALL_TAG  install this tag instead of the newest release,
#                             e.g. a release candidate v0.28.0-rc.20261111
#                             (checksum-verified the same way)
#   GITHUB_TOKEN              optional token (avoids API rate limits)
set -eu

REPO="BeppeTemp/cartographer"
API_URL="https://api.github.com/repos/${REPO}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download"
BIN_NAME="cartographer"

log() { printf '%s\n' "$*" >&2; }
fail() { log "error: $*"; exit 1; }

# fetch URL (to stdout) or fetch -o FILE URL: curl when there is one, else wget
# (GNU and busybox take -q and -O alike). Both are limited to https, redirects
# included. The token goes through curl only: curl drops it on a cross-host
# redirect (the asset CDN), wget would forward it.
fetch() {
    if command -v curl >/dev/null 2>&1; then
        if [ -n "${GITHUB_TOKEN:-}" ]; then
            curl -fsSL --proto '=https' --proto-redir '=https' -H "Authorization: Bearer ${GITHUB_TOKEN}" "$@"
        else
            curl -fsSL --proto '=https' --proto-redir '=https' "$@"
        fi
        return
    fi
    out=-
    if [ "${1:-}" = "-o" ]; then out=$2; shift 2; fi
    # WGET_HTTPS is --https-only where wget has it (GNU). A busybox wget has no
    # such flag and no way to refuse a redirect: every URL this script fetches
    # is an https constant, so it is what we can do there.
    wget -q ${WGET_HTTPS:+"$WGET_HTTPS"} -O "$out" "$@"
}

# need_fetcher fails before any work when there is neither curl nor wget, and
# sets WGET_HINT so a busybox wget without TLS reads as what it is.
need_fetcher() {
    WGET_HINT=""
    command -v curl >/dev/null 2>&1 && return 0
    command -v wget >/dev/null 2>&1 || fail "need curl or wget"
    WGET_HTTPS=""
    if wget --help 2>&1 | grep -q -- '--https-only'; then WGET_HTTPS="--https-only"; fi
    [ -z "${GITHUB_TOKEN:-}" ] || log "note: GITHUB_TOKEN is not used with wget (it would follow redirects to other hosts with it); install curl to use it"
    WGET_HINT=" (if this is a TLS error: wget without HTTPS support, install curl or ca-certificates + ssl_client)"
}

# sha256_tool names the first SHA-256 tool this host has, empty when none.
sha256_tool() {
    if command -v sha256sum >/dev/null 2>&1; then echo sum
    elif command -v shasum >/dev/null 2>&1; then echo shasum
    elif command -v openssl >/dev/null 2>&1; then echo openssl
    elif busybox sha256sum </dev/null >/dev/null 2>&1; then echo busybox
    fi
}

# sha256_of FILE prints the hex digest (sha256_tool must not be empty).
sha256_of() {
    case "$(sha256_tool)" in
        sum) sha256sum "$1" | cut -d' ' -f1 ;;
        shasum) shasum -a 256 "$1" | cut -d' ' -f1 ;;
        openssl) openssl dgst -sha256 "$1" | sed 's/^.*[[:space:]]//' ;;
        busybox) busybox sha256sum "$1" | cut -d' ' -f1 ;;
    esac
}

detect_target() {
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)
    # The OS is decided before the architecture, so a Windows shell gets the
    # install.ps1 instruction whatever `uname -m` says there.
    case "$os" in
        darwin|linux) ;;
        # Git Bash, MSYS2 and Cygwin report mingw64_nt-10.0, msys_nt-10.0 and
        # cygwin_nt-10.0, so a Windows user piping this script lands here rather
        # than on the generic refusal below. install.ps1 is the Windows installer
        # (D252), for `update` and `uninstall` as well as `install`, so naming it
        # is the whole answer.
        mingw*|msys*|cygwin*|windows*)
            log "Windows is installed, upgraded and removed with install.ps1, from PowerShell:"
            log ""
            log "  irm https://raw.githubusercontent.com/${REPO}/main/install.ps1 | iex"
            log ""
            log "update and uninstall: see the README (\"Install\")."
            exit 1
            ;;
        *) fail "unsupported OS: $os" ;;
    esac
    case "$arch" in
        x86_64|amd64) arch=amd64 ;;
        arm64|aarch64)
            arch=arm64
            # A 64-bit kernel under a 32-bit userland (common on Raspberry Pi
            # images) cannot run the arm64 binary.
            if [ "$os" = linux ] && [ "$(getconf LONG_BIT 2>/dev/null || true)" = 32 ]; then arch=arm; fi
            ;;
        armv7*|armv8l) arch=arm ;;
        armv6*|armv5*) fail "unsupported architecture: $arch (the 32-bit ARM build needs ARMv7)" ;;
        *) fail "unsupported architecture: $arch" ;;
    esac
    printf '%s-%s' "$os" "$arch"
}

install_dir() {
    dir="${CARTOGRAPHER_INSTALL_DIR:-/usr/local/bin}"
    if [ ! -w "$dir" ] 2>/dev/null; then
        if [ -z "${CARTOGRAPHER_INSTALL_DIR:-}" ]; then
            dir="${HOME}/.local/bin"
            mkdir -p "$dir"
        else
            fail "install dir not writable: $dir"
        fi
    fi
    printf '%s' "$dir"
}

latest_tag() {
    # /releases, not /releases/latest: the latter is documented to skip
    # pre-releases, and every 0.x release of this project is one (D82, D211).
    # Against /releases/latest this installer would 404 while no stable release
    # exists, and — worse — quietly keep installing the last stable one once one
    # does. The list endpoint is ordered newest-first and includes pre-releases;
    # drafts are not published here. A release candidate (a tag with a semver
    # pre-release suffix, v0.28.0-rc.20261111, D1192) is skipped by its tag,
    # never by the `prerelease` flag, which stable 0.x releases carry: a hundred
    # entries (one page, GitHub's maximum) leave room for the candidates, which
    # stay until their version ships (D1384). The read is not paginated: with a
    # hundred newer candidates, no stable tag is found and the message says so,
    # distinct from a failed request. One key per line first, so compact and
    # pretty-printed JSON read the same.
    list=$(fetch "${API_URL}/releases?per_page=100") \
        || fail "cannot determine latest release (rate-limited? set GITHUB_TOKEN)${WGET_HINT}"
    tag=$(printf '%s\n' "$list" | tr ',{' '\n\n' \
        | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' \
        | grep -E '^v?[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)
    [ -n "$tag" ] || fail "no stable release among the newest 100 releases (only release candidates?): set CARTOGRAPHER_INSTALL_TAG"
    printf '%s' "$tag"
}

# warn_no_git prints one warning when git is unusable, never fails: a KB is a
# git repository, so the first `cartographer setup` stops without it. On macOS
# without the Command Line Tools /usr/bin/git is a shim that is on the PATH
# yet opens the installer dialog when run, so there the check is
# `xcode-select -p`, never `git --version`.
warn_no_git() {
    os=$(uname -s)
    if [ "$os" = Darwin ]; then
        xcode-select -p >/dev/null 2>&1 && return 0
    else
        command -v git >/dev/null 2>&1 && return 0
    fi
    log ""
    log "warning: git is not installed — Cartographer needs it for every KB."
    case "$os" in
        Darwin) log "  xcode-select --install     (then run cartographer setup again)" ;;
        *) log "  install git with your package manager (apt, dnf, pacman, apk...)." ;;
    esac
}

do_install() {
    need_fetcher
    target=$(detect_target)
    if [ -n "${CARTOGRAPHER_INSTALL_TAG:-}" ]; then
        # The tag goes into a URL: only the shape of a release tag is accepted.
        tag=$CARTOGRAPHER_INSTALL_TAG
        case "$tag" in *[!0-9A-Za-z.-]*) fail "CARTOGRAPHER_INSTALL_TAG is not a release tag: $tag" ;; esac
        printf '%s\n' "$tag" | grep -Eqx 'v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?' \
            || fail "CARTOGRAPHER_INSTALL_TAG is not a release tag: $tag"
    else
        tag=$(latest_tag)
    fi
    [ -n "$tag" ] || fail "cannot determine latest release (rate-limited? set GITHUB_TOKEN)"
    dir=$(install_dir)
    dest="${dir}/${BIN_NAME}"

    fresh=1
    if [ -x "$dest" ]; then
        fresh=0
        current=$("$dest" version 2>/dev/null || echo "unknown")
        if [ "$current" = "$tag" ]; then
            log "cartographer ${tag} already installed at ${dest}"
            warn_no_git
            return 0
        fi
        log "updating cartographer ${current} -> ${tag}"
    else
        log "installing cartographer ${tag} to ${dest}"
    fi

    asset="cartographer-${target}"
    url="${DOWNLOAD_URL}/${tag}/${asset}"
    # Next to the destination, so the final mv is a rename on one filesystem (an
    # interrupt never leaves a truncated executable), and removed on every exit.
    tmpdir=$(mktemp -d "${dir}/.cartographer-install.XXXXXX") || fail "cannot create a temporary directory in ${dir}"
    trap 'rm -rf "$tmpdir"' EXIT
    tmp="${tmpdir}/${BIN_NAME}"
    fetch -o "$tmp" "$url" || fail "download failed: ${url}${WGET_HINT}"

    # Every release since v0.1.0 publishes sha256sums.txt, so a manifest that
    # cannot be fetched (404, network error) is never "an old tag": it is the
    # shape of an attack that drops that one request to skip verification, or of
    # a broken release. Either way nothing is installed without that check
    # (D1022, as install.ps1 does). It covers corruption and a truncated or
    # swapped download, not a tampered release: the manifest comes from the same
    # release as the binary. The signed attestation (D1022) is verified with
    # `gh attestation verify`, by no installer. A manifest that exists but does not cover this asset is
    # the same error.
    fetch -o "$tmpdir/sha256sums.txt" "${DOWNLOAD_URL}/${tag}/sha256sums.txt" 2>/dev/null \
        || fail "cannot download sha256sums.txt for ${tag}: refusing to install unverified${WGET_HINT}"
    expected=$(grep " ${asset}\$" "$tmpdir/sha256sums.txt" | cut -d' ' -f1)
    [ -n "$expected" ] || fail "sha256sums.txt has no entry for ${asset}: refusing to install unverified"
    [ -n "$(sha256_tool)" ] || fail "no sha256 tool: refusing to install unverified"
    actual=$(sha256_of "$tmp")
    [ "$actual" = "$expected" ] || fail "checksum mismatch for ${asset}"
    log "checksum OK"

    chmod +x "$tmp"
    mv "$tmp" "$dest"
    log "installed: $("$dest" version) -> ${dest}"
    case ":$PATH:" in
        *":${dir}:"*) ;;
        *)
            log "note: ${dir} is not in your PATH — either invoke it by path:"
            log "        ${dest} version"
            log "      or add it to your PATH, e.g.:"
            log "        echo 'export PATH=\"${dir}:\$PATH\"' >> ~/.profile && . ~/.profile"
            ;;
    esac

    # Repair any native service in place (D121): upgrade-repair gracefully
    # replaces an already-running service, proves the new version is serving,
    # and reconciles configured providers. It is a no-op when the service is
    # stopped or not installed, so it never starts something deliberately
    # left off.
    rc=0
    "$dest" upgrade-repair || rc=$?
    case "$rc" in
        0) ;;
        1)
            log "provider sync is pending — the binary update succeeded; retry with: ${dest} sync"
            ;;
        2)
            fail "new binary installed at ${dest} but the running native service could not be verified — inspect it manually (e.g. \`${dest} service status\`) and retry \`${dest} upgrade-repair\`"
            ;;
        *)
            fail "unexpected exit code ${rc} from '${dest} upgrade-repair' — inspect the service manually and retry"
            ;;
    esac

    # A first install ends on the one command that does the rest (D253).
    if [ "$fresh" = 1 ]; then
        log ""
        log "Next: cartographer setup   (server, first knowledge base and agents, in one guided step)"
    fi
    warn_no_git
}

# leftover_units lists the native units this machine still has installed, one
# per line. A coordinated teardown that removes a user's KB data is not
# something an installer should do implicitly, so uninstall names them instead.
leftover_units() {
    for unit in \
        "${HOME}/Library/LaunchAgents/com.cartographer.serve.plist" \
        "${HOME}/Library/LaunchAgents/com.cartographer.sync.plist" \
        "${HOME}/Library/LaunchAgents/com.cartographer.tray.plist" \
        "${HOME}/.config/systemd/user/cartographer.service" \
        "${HOME}/.config/systemd/user/cartographer-sync.timer" \
        "${HOME}/.config/cartographer/cron/server.cron" \
        "${HOME}/.config/cartographer/cron/sync.cron"
    do
        [ -f "$unit" ] && echo "$unit"
    done
    return 0
}

do_uninstall() {
    dir=$(install_dir)
    dest="${dir}/${BIN_NAME}"

    units=$(leftover_units)
    if [ -n "$units" ] && [ "$BINARY_ONLY" != "1" ]; then
        log "this machine still has Cartographer units installed:"
        echo "$units" | while IFS= read -r unit; do log "  ${unit}"; done
        log ""
        log "removing only the binary would leave them pointing at a missing executable."
        log "Remove them first, with the binary still in place:"
        log "  cartographer service sync-timer uninstall"
        log "  cartographer service tray uninstall"
        log "  cartographer service uninstall"
        log "  cartographer disconnect            # removes the artifacts materialized into your agents"
        log ""
        log "then rerun: $0 uninstall"
        log "Or, to delete the binary anyway and clean up by hand later:"
        log "  $0 uninstall --binary-only"
        exit 1
    fi

    if [ -x "$dest" ]; then
        rm -f "$dest"
        log "removed ${dest}"
    else
        log "cartographer not found in ${dir}, nothing to do"
    fi
    log "note: this removes the binary only — materialized agent artifacts and your KB data are untouched."
}

cmd="${1:-install}"
BINARY_ONLY=0
[ "${2:-}" = "--binary-only" ] && BINARY_ONLY=1
case "$cmd" in
    install|update) do_install ;;
    uninstall) do_uninstall ;;
    *) fail "unknown command: $cmd (want install|update|uninstall [--binary-only])" ;;
esac
