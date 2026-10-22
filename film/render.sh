#!/usr/bin/env bash
# Renders the README film headless (D1068): starts the film's Vite server, runs film/record.mjs against it and
# stops the server whatever happens. Writes film/out/film.webp (and film/out/film.mp4 unless --no-mp4).
#
# Usage, from anywhere: film/render.sh [record.mjs options, e.g. --no-mp4]
# Needs `cd web && npm ci` and Chromium (npx playwright install chromium), plus ffmpeg and img2webp (webp package).
# bash 3.2 compatible: it runs on macOS too.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
mkdir -p film/out
LOG="film/out/vite.log"
URL="http://localhost:5180/"

# Vite's own binary, not `npx vite`: the pid killed below is the server itself, not a wrapper that would orphan it.
(cd web && exec ./node_modules/.bin/vite --config ../film/vite.config.ts) >"$LOG" 2>&1 &
SERVER=$!
trap 'kill "$SERVER" 2>/dev/null || true' EXIT

up=""
for _ in $(seq 1 60); do
    if curl -fsS -o /dev/null "$URL" 2>/dev/null; then up=1; break; fi
    kill -0 "$SERVER" 2>/dev/null || break
    sleep 1
done
if [ -z "$up" ]; then
    echo "render: the vite server did not answer on ${URL}; last lines of ${LOG}:" >&2
    tail -n 20 "$LOG" >&2
    exit 1
fi

rm -f film/out/film.webp
node film/record.mjs "$@"

[ -s film/out/film.webp ] || { echo "render: film/out/film.webp is missing or empty" >&2; exit 1; }
echo "render: film/out/film.webp is $(wc -c < film/out/film.webp | tr -d ' ') bytes"
