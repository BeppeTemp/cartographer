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

    # Verify checksum when the release ships sha256sums.txt. A release with no
    # such file is still installable (older tags have none); a file that exists
    # but does not cover this asset is an error, not a skip — it is the shape a
    # tampered or truncated manifest has.
    if auth_curl -o "$tmp.sha" "${DOWNLOAD_URL}/${tag}/sha256sums.txt" 2>/dev/null; then
        expected=$(grep " ${asset}\$" "$tmp.sha" | cut -d' ' -f1)
        [ -n "$expected" ] || fail "sha256sums.txt has no entry for ${asset}: refusing to install unverified"
        actual=$( (sha256sum "$tmp" 2>/dev/null || shasum -a 256 "$tmp") | cut -d' ' -f1)
        [ "$actual" = "$expected" ] || fail "checksum mismatch for ${asset}"
        log "checksum OK"
    fi

    chmod +x "$tmp"
    mv "$tmp" "$dest"
    trap - EXIT
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
}

# leftover_units lists the native units this machine still has installed, one
# per line. A coordinated teardown that removes a user's KB data is not
# something an installer should do implicitly, so uninstall names them instead.
leftover_units() {
    for unit in \
        "${HOME}/Library/LaunchAgents/com.cartographer.serve.plist" \
        "${HOME}/Library/LaunchAgents/com.cartographer.sync.plist" \
        "${HOME}/.config/systemd/user/cartographer.service" \
        "${HOME}/.config/systemd/user/cartographer-sync.timer"
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
