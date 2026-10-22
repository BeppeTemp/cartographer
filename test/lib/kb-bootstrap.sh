#!/usr/bin/env bash
# kb-bootstrap.sh <dir>... — make each folder a KB the server can mount.
#
# `serve` never creates a KB (D906), so a fixture or a scratch folder a test
# serves has to be one already. The scaffold is made by `cartographer kb create
# --no-remote` in a scratch directory, whatever the folder holds (a fixture, an
# empty `mcp/`) is laid over it, and the whole is committed. A folder that is
# already a git repository is left alone. Needs bin/cartographer (make build).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN="${CARTOGRAPHER_BIN:-${REPO_ROOT}/bin/cartographer}"
[ -x "$BIN" ] || { echo "kb-bootstrap: missing $BIN: run 'make build' first" >&2; exit 1; }

for dir in "$@"; do
    dir="${dir%/}"
    [ -e "$dir/.git" ] && continue
    name="$(basename "$dir")"
    scratch="$(mktemp -d)"
    if ! "$BIN" kb create "$name" --no-remote --local --data "$scratch" >/dev/null 2>&1; then
        echo "kb-bootstrap: kb create failed for ${dir}" >&2
        rm -rf "$scratch"
        exit 1
    fi
    if [ -d "$dir" ]; then
        cp -R "$dir/." "$scratch/$name/"
        rm -rf "$dir"
    fi
    mkdir -p "$(dirname "$dir")"
    mv "$scratch/$name" "$dir"
    rmdir "$scratch"
    git -C "$dir" add -A
    if ! git -C "$dir" diff --cached --quiet; then
        git -C "$dir" -c user.name=fixture -c user.email=fixture@example.com commit -q -m "fixture: seed"
    fi
done
