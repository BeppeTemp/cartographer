#!/usr/bin/env bash
# Prints the directory scripts/dev.sh keeps the generated test KBs in, after making
# sure it exists, is owned by the caller and is mode 700. It lives under the user's
# own cache directory, never under a shared, predictable path such as /tmp: the
# server's git runs files from what it finds there.
set -euo pipefail
dir="${XDG_CACHE_HOME:-$HOME/.cache}/cartographer-testkbs"
mkdir -p "$dir"
[ -O "$dir" ] || { echo "$dir is not owned by you" >&2; exit 1; }
chmod 700 "$dir"
printf '%s\n' "$dir"
