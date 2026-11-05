#!/bin/sh
# lib/fake-wget.sh — wget stand-in: translates `wget -q [--header H] -O FILE URL`
# into the call fake-curl.sh answers (`-o FILE URL`; -O - means stdout).
out=""
url=""
while [ $# -gt 0 ]; do
    case "$1" in
        -q) ;;
        --header) shift ;;
        -O) shift; out=$1 ;;
        *) url=$1 ;;
    esac
    shift
done
if [ "$out" = "-" ] || [ -z "$out" ]; then
    exec "$(dirname "$0")/fake-curl-impl" "$url"
fi
exec "$(dirname "$0")/fake-curl-impl" -o "$out" "$url"
