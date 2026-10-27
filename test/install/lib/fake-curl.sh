#!/bin/sh
# lib/fake-curl.sh — network-free curl stand-in for the test/install/ suite.
# Installed as `curl` at the front of PATH so install.sh's auth_curl() calls
# land here instead of the network. Recognizes exactly the three requests
# install.sh makes: the release-list API call, the asset download, and the
# sha256sums.txt (see FAKE_SHA_MODE).
#
# Driven by env vars set by lib/harness.sh:
#   FAKE_TAG          tag_name returned for the release-list API call
#   FAKE_RC_TAGS      optional release candidates (space-separated) listed
#                     before FAKE_TAG, newest first, as GitHub orders them (D1192)
#   FAKE_NEW_BINARY   path to the fixture copied in as the "downloaded" asset
#   FAKE_SHA_MODE     how to answer the sha256sums.txt request (D192):
#                       absent   — 404: a release whose manifest cannot be fetched (D1022)
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
    # A JSON array, matching the endpoint install.sh actually calls: /releases
    # lists pre-releases, /releases/latest does not, and every 0.x release of
    # this project is one (D211). Answering the old path here would let the
    # installer regress back to it without a test noticing.
    # Compact JSON on one line, as the API may send it: the installer must not
    # depend on one key per line. Every call is logged, so a scenario can
    # assert the list was not read.
    *"/releases?per_page=10")
        echo "$url" >> "${SCENARIO_TMP:-/tmp}/release-list-calls"
        list=""
        for rc in ${FAKE_RC_TAGS:-}; do
            list="${list}{\"tag_name\":\"${rc}\",\"prerelease\":true,\"body\":\"candidate, not \\\"tag_name\\\":\\\"v0.0.1\\\"\"},"
        done
        printf '[%s{"tag_name":"%s","prerelease":true}]\n' "$list" "${FAKE_TAG:?FAKE_TAG not set}"
        ;;
    *"sha256sums.txt")
        # The asset request always precedes this one, and it recorded the
        # exact asset name install.sh derived for this platform.
        asset=$(cat "${SCENARIO_TMP:-/tmp}/last-asset" 2>/dev/null || echo "unknown-asset")
        case "${FAKE_SHA_MODE:-valid}" in
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
        echo "$url" > "${SCENARIO_TMP:-/tmp}/last-asset-url" 2>/dev/null || true
        cp "${FAKE_NEW_BINARY:?FAKE_NEW_BINARY not set}" "$out"
        ;;
esac
