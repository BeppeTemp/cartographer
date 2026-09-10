#!/bin/sh
# lib/fake-curl.sh — network-free curl stand-in for the test/install/ suite.
# Installed as `curl` at the front of PATH so install.sh's auth_curl() calls
# land here instead of the network. Recognizes exactly the three requests
# install.sh makes: the latest-release API call, the asset download, and the
# optional sha256sums.txt (see FAKE_SHA_MODE).
#
# Driven by env vars set by lib/harness.sh:
#   FAKE_TAG          tag_name returned for the "latest release" API call
#   FAKE_NEW_BINARY   path to the fixture copied in as the "downloaded" asset
#   FAKE_SHA_MODE     how to answer the sha256sums.txt request (D192):
#                       absent   — 404, as before: a release that ships none
#                       valid    — the real sha256 of FAKE_NEW_BINARY
#                       mismatch — a well-formed line with the wrong digest
#                       noasset  — a file that covers some other asset only

out=""
url=""
prev=""
for arg in "$@"; do
    if [ "$prev" = "-o" ]; then
        out="$arg"
    fi
    case "$arg" in
        http*://*) url="$arg" ;;
    esac
    prev="$arg"
done

case "$url" in
    *"/releases/latest")
        printf '{"tag_name": "%s"}\n' "${FAKE_TAG:?FAKE_TAG not set}"
        ;;
    *"sha256sums.txt")
        # The asset request always precedes this one, and it recorded the
        # exact asset name install.sh derived for this platform.
        asset=$(cat "${SCENARIO_TMP:-/tmp}/last-asset" 2>/dev/null || echo "unknown-asset")
        case "${FAKE_SHA_MODE:-absent}" in
            valid)
                digest=$( (sha256sum "${FAKE_NEW_BINARY}" 2>/dev/null || shasum -a 256 "${FAKE_NEW_BINARY}") | cut -d' ' -f1)
                printf '%s  %s\n' "$digest" "$asset" > "${out:-/dev/stdout}"
                ;;
            mismatch)
                printf '%s  %s\n' "0000000000000000000000000000000000000000000000000000000000000000" "$asset" > "${out:-/dev/stdout}"
                ;;
            noasset)
                printf '%s  %s\n' "1111111111111111111111111111111111111111111111111111111111111111" "some-other-asset" > "${out:-/dev/stdout}"
                ;;
            *)
                exit 22
                ;;
        esac
        ;;
    *)
        if [ -z "$out" ]; then
            echo "fake-curl: unhandled request: $url" >&2
            exit 1
        fi
        basename "$url" > "${SCENARIO_TMP:-/tmp}/last-asset" 2>/dev/null || true
        cp "${FAKE_NEW_BINARY:?FAKE_NEW_BINARY not set}" "$out"
        ;;
esac
