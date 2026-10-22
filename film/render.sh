#!/usr/bin/env bash
# Renders the README film headless (D1068): starts the vite server, runs film/record.mjs, stops the server.
# Writes film/out/film.webp; film.yml (CI) and a Mac both run it. Extra arguments go to record.mjs
# (CI passes --no-mp4). Needs `npm ci` in web/, a Playwright Chromium, ffmpeg and img2webp (webp package).
# Run from anywhere; bash 3.2 compatible.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG="$(mktemp)"
PID=""
cleanup() {
    [ -z "$PID" ] || kill "$PID" 2>/dev/null || true
    rm -f "$LOG"
}
trap cleanup EXIT

(cd "$ROOT/web" && exec npx vite --config ../film/vite.config.ts) >"$LOG" 2>&1 &
PID=$!

# strictPort: a second server on 5180 fails instead of moving, so the wait below cannot see a stranger's server.
up=""
for _ in $(seq 1 60); do
    if curl -fsS -o /dev/null http://localhost:5180/ 2>/dev/null; then up=1; break; fi
    kill -0 "$PID" 2>/dev/null || break
    sleep 1
done
if [ -z "$up" ]; then
    echo "render: the vite server did not come up on :5180, its log ends:" >&2
    tail -n 30 "$LOG" >&2
    exit 1
fi

node "$ROOT/film/record.mjs" "$@"

[ -s "$ROOT/film/out/film.webp" ] || { echo "render: film/out/film.webp is missing or empty" >&2; exit 1; }
echo "render: $(wc -c <"$ROOT/film/out/film.webp") bytes in film/out/film.webp"
