#!/bin/sh
# lib/fake-wget.sh — wget stand-in: translates `wget -q [--header H] -O FILE URL`
# into the call fake-curl.sh answers (`-o FILE URL`; -O - means stdout).
# FAKE_WGET_BUSYBOX: a wget whose --help does not know --https-only.
if [ "${1:-}" = "--help" ]; then
    [ -n "${FAKE_WGET_BUSYBOX:-}" ] || echo "      --https-only  only follow secure HTTPS links"
    exit 0
fi
echo "$*" >> "${SCENARIO_TMP:-/tmp}/wget-argv"
out=""
url=""
while [ $# -gt 0 ]; do
    case "$1" in
        -q) ;;
        --header) shift ;;
        --https-only) ;;
        -O) shift; out=$1 ;;
        *) url=$1 ;;
    esac
    shift
done
if [ "$out" = "-" ] || [ -z "$out" ]; then
    exec "$(dirname "$0")/fake-curl-impl" "$url"
fi
exec "$(dirname "$0")/fake-curl-impl" -o "$out" "$url"
