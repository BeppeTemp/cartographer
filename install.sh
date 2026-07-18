#!/bin/sh
# Cartographer client installer — install / update / uninstall.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- update
#   curl -fsSL .../install.sh | sh -s -- uninstall
#
# On macOS, Homebrew is the preferred install method:
#   brew install beppetemp/tap/cartographer
#
# Environment:
#   CARTOGRAPHER_INSTALL_DIR  target directory (default: /usr/local/bin, falls
#                             back to ~/.local/bin when not writable)
#   GITHUB_TOKEN              optional token (avoids API rate limits)
set -eu

REPO="BeppeTemp/cartographer"
API_URL="https://api.github.com/repos/${REPO}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download"
BIN_NAME="cartographer"

log() { printf '%s\n' "$*" >&2; }
fail() { log "error: $*"; exit 1; }

auth_curl() {
    if [ -n "${GITHUB_TOKEN:-}" ]; then
        curl -fsSL -H "Authorization: Bearer ${GITHUB_TOKEN}" "$@"
    else
        curl -fsSL "$@"
    fi
}

detect_target() {
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)
    case "$arch" in
        x86_64|amd64) arch=amd64 ;;
        arm64|aarch64) arch=arm64 ;;
        *) fail "unsupported architecture: $arch" ;;
    esac
    case "$os" in
        darwin|linux) ;;
        *) fail "unsupported OS: $os" ;;
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
    auth_curl "${API_URL}/releases/latest" \
        | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -1
}

do_install() {
    target=$(detect_target)
    tag=$(latest_tag)
    [ -n "$tag" ] || fail "cannot determine latest release (rate-limited? set GITHUB_TOKEN)"
    dir=$(install_dir)
    dest="${dir}/${BIN_NAME}"

    if [ -x "$dest" ]; then
        current=$("$dest" version 2>/dev/null || echo "unknown")
        if [ "$current" = "$tag" ]; then
            log "cartographer ${tag} already installed at ${dest}"
            return 0
        fi
        log "updating cartographer ${current} -> ${tag}"
    else
        log "installing cartographer ${tag} to ${dest}"
    fi

    asset="cartographer-${target}"
    url="${DOWNLOAD_URL}/${tag}/${asset}"
    tmp=$(mktemp)
    trap 'rm -f "$tmp" "$tmp.sha"' EXIT
    auth_curl -o "$tmp" "$url" || fail "download failed: $url"

    # Verify checksum when the release ships sha256sums.txt.
    if auth_curl -o "$tmp.sha" "${DOWNLOAD_URL}/${tag}/sha256sums.txt" 2>/dev/null; then
        expected=$(grep " ${asset}\$" "$tmp.sha" | cut -d' ' -f1)
        if [ -n "$expected" ]; then
            actual=$( (sha256sum "$tmp" 2>/dev/null || shasum -a 256 "$tmp") | cut -d' ' -f1)
            [ "$actual" = "$expected" ] || fail "checksum mismatch for ${asset}"
            log "checksum OK"
        fi
    fi

    chmod +x "$tmp"
    mv "$tmp" "$dest"
    trap - EXIT
    log "installed: $("$dest" version) -> ${dest}"
    case ":$PATH:" in
        *":${dir}:"*) ;;
        *) log "note: ${dir} is not in your PATH" ;;
    esac

    # Restart the native service only if it is currently running (status rc 0):
    # a deliberately stopped service (rc 3) must stay stopped after an update,
    # and `launchctl kickstart` fails on a job that is not loaded anyway.
    rc=0
    "$dest" service status >/dev/null 2>&1 || rc=$?
    if [ "$rc" -eq 0 ]; then
        log "restarting cartographer service"
        "$dest" service restart
    fi
}

do_uninstall() {
    dir=$(install_dir)
    dest="${dir}/${BIN_NAME}"
    if [ -x "$dest" ]; then
        rm -f "$dest"
        log "removed ${dest}"
    else
        log "cartographer not found in ${dir}, nothing to do"
    fi
}

cmd="${1:-install}"
case "$cmd" in
    install|update) do_install ;;
    uninstall) do_uninstall ;;
    *) fail "unknown command: $cmd (want install|update|uninstall)" ;;
esac
