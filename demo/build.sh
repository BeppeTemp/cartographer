#!/usr/bin/env bash
# Builds the static Atlas demo (D745) into <out-dir>: the Atlas bundle in
# static mode plus the JSON answers of a real server over demo/astronomy.
#
# Usage: demo/build.sh <out-dir>
# Needs: Go, Node (web/.nvmrc), git, curl. Run from anywhere.
#
# The KB is replayed into a throwaway git repository by demo/replay.sh.
set -euo pipefail

out=${1:?usage: demo/build.sh <out-dir>}
repo=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$out"
out=$(cd "$out" && pwd)
work=$(mktemp -d)
pid=
cleanup() {
  [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

# --- the KB, as a git history -------------------------------------------------
kb="$work/astronomy"
"$repo/demo/replay.sh" "$kb"

# --- the Atlas bundle, static mode ----------------------------------------------
cd "$repo/web"
[ -d node_modules ] || npm ci --silent
VITE_ATLAS_STATIC=1 npx vite build --outDir "$out" --emptyOutDir --logLevel warn

# --- a real server, exported ----------------------------------------------------
cd "$repo"
# The version the top bar shows: the release the site was built from.
version=$(git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || true)
go build -ldflags "-X main.version=${version:-dev}" -o "$work/cartographer" ./cmd/cartographer
port=$((40000 + RANDOM % 20000))
"$work/cartographer" serve --kb "$kb" --http "127.0.0.1:$port" >"$work/serve.log" 2>&1 &
pid=$!
url="http://127.0.0.1:$port"
for _ in $(seq 100); do
  curl -sf "$url/api/ui/v1/kbs" 2>/dev/null | grep -q '"ready":true' && break
  sleep 0.2
done
curl -sf "$url/api/ui/v1/kbs" | grep -q '"ready":true' || { cat "$work/serve.log" >&2; exit 1; }
node "$repo/demo/export.mjs" "$url" astronomy "$out"
echo "demo: static Atlas in $out"
