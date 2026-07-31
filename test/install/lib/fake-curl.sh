#!/bin/sh
# lib/fake-curl.sh — network-free curl stand-in for the test/install/ suite.
# Installed as `curl` at the front of PATH so install.sh's auth_curl() calls
# land here instead of the network. Recognizes exactly the three requests
# install.sh makes: the latest-release API call, the asset download, and the
# optional sha256sums.txt (always reports "not found" — checksum verification
# is skipped, it is out of scope for this suite).
#
# Driven by env vars set by lib/harness.sh:
#   FAKE_TAG          tag_name returned for the "latest release" API call
#   FAKE_NEW_BINARY   path to the fixture copied in as the "downloaded" asset

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
        exit 22
        ;;
    *)
        if [ -z "$out" ]; then
            echo "fake-curl: unhandled request: $url" >&2
            exit 1
        fi
        cp "${FAKE_NEW_BINARY:?FAKE_NEW_BINARY not set}" "$out"
        ;;
esac
