#!/usr/bin/env bash
# The Cask GoReleaser renders, installed by real Homebrew from a local tap
# (D997). Only the Cask's `url` is rewritten (to the snapshot binary on disk);
# everything else, the sha256, the binary stanza, the postflight and the
# caveats, is what a release publishes.
#
# Usage: test/macos/cask.sh <dist dir from `goreleaser release --snapshot`>
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=lib.sh
. "$here/lib.sh"
mac_guard

dist=${1:-dist}
dist=$(cd "$dist" && pwd)
case $(uname -m) in arm64) arch=arm64 ;; *) arch=amd64 ;; esac

cask_src=$(find "$dist" -name cartographer.rb -path '*Casks*' | head -n 1)
[ -n "$cask_src" ] || { echo "no rendered Cask under $dist (goreleaser snapshot did not render it)" >&2; exit 1; }
# GoReleaser keeps the raw binary under dist/cartographer_darwin_<arch>_<level>/
# (the release asset cartographer-darwin-<arch> is that file renamed).
asset=$(find "$dist" -maxdepth 2 -type f -path "*/cartographer_darwin_${arch}*/cartographer" | head -n 1)
[ -n "$asset" ] || { echo "no darwin $arch binary under $dist" >&2; exit 1; }

tap=local/cart-test
cleanup() {
  local rc=$?
  trap - EXIT
  if [ "$rc" -eq 0 ] && [ "${MAC_DONE:-0}" != 1 ]; then echo "FAIL: the script ended before its last line" >&2; rc=1; fi
  brew uninstall --cask --force "$tap/cartographer" >/dev/null 2>&1 || true
  brew untap "$tap" >/dev/null 2>&1 || true
  exit "$rc"
}
trap cleanup EXIT

if brew list --cask cartographer >/dev/null 2>&1; then mac_fail "a cartographer cask is already installed here"; fi

echo "→ the local tap"
export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_FROM_API=1 HOMEBREW_NO_ANALYTICS=1
brew_out brew tap-new "$tap" --no-git >/dev/null
tap_dir=$(brew_out brew --repository "$tap")
mkdir -p "$tap_dir/Casks"

# The Cask's binary stanza names the downloaded file after the release asset
# (cartographer-darwin-<arch>), and Homebrew names it after the url's basename:
# serve a copy under that name, the same bytes.
stage=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/cartographer-cask.XXXXXX")
cp "$asset" "$stage/cartographer-darwin-$arch"
asset="$stage/cartographer-darwin-$arch"
# Positive control: the downloaded file carries the quarantine attribute, as a
# browser download does, so its absence after install can only be the
# postflight step's doing.
xattr -w com.apple.quarantine "0081;00000000;test;" "$asset"
want=$(shasum -a 256 "$asset" | cut -d' ' -f1)
grep -q "$want" "$cask_src" || mac_fail "the Cask sha256 is not the snapshot binary's ($want)"
mac_ok "Cask sha256 matches $(basename "$asset")"
# Only the url changes.
sed "s|^\\([[:space:]]*url \\).*|\\1\"file://$asset\"|" "$cask_src" >"$tap_dir/Casks/cartographer.rb"
other=$(diff "$cask_src" "$tap_dir/Casks/cartographer.rb" | grep '^[<>]' | grep -v 'url ' || true)
[ -z "$other" ] || mac_fail "more than the url differs from the rendered Cask: $other"
grep -q "file://$asset" "$tap_dir/Casks/cartographer.rb" || mac_fail "the url was not rewritten"

# Every brew call here fails through mac_fail with its own output (#1386): a bare
# failure under set -e ended the script with no message. One bounded retry,
# because the runner's Homebrew can fail transiently.
brew_out() {
  local out
  out=$("$@" 2>&1) && { printf '%s' "$out"; return 0; }
  echo "retrying once after: $out" >&2
  sleep 5
  out=$("$@" 2>&1) || mac_fail "$* failed: $out"
  printf '%s' "$out"
}

echo "→ install"
caveats=$(brew_out brew info --cask "$tap/cartographer")
case $caveats in *"cartographer setup"*) ;; *) mac_fail "caveats do not name 'cartographer setup'" ;; esac
mac_ok "caveats name cartographer setup"
if ! xattr -p com.apple.quarantine "$asset" >/dev/null 2>&1; then mac_fail "the control attribute was not set on $asset"; fi
brew install --cask "$tap/cartographer" || mac_fail "brew install --cask failed"
shim="$(brew --prefix)/bin/cartographer"
[ -x "$shim" ] || mac_fail "no $shim after install"
"$shim" version >/dev/null || mac_fail "the installed binary does not run"
mac_ok "installed: $("$shim" version)"
real=$(readlink "$shim" 2>/dev/null || echo "$shim")
case $real in /*) ;; *) real="$(dirname "$shim")/$real" ;; esac
if xattr -p com.apple.quarantine "$real" >/dev/null 2>&1; then
  mac_fail "$real still carries com.apple.quarantine: the postflight step did not run"
fi
mac_ok "quarantine set on the download is gone (postflight_steps ran)"

echo "→ uninstall"
brew uninstall --cask "$tap/cartographer" || mac_fail "brew uninstall --cask failed"
[ ! -e "$shim" ] || mac_fail "the PATH shim survived the uninstall"
[ ! -e "$(brew --caskroom)/cartographer" ] || mac_fail "the Caskroom entry survived the uninstall"
mac_ok "binary, shim and Caskroom entry removed"

mac_pass cask.sh
