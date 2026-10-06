#!/usr/bin/env bash
# Records the README's hero animation (D330): e2e/hero.spec.ts walked over the
# demo KB at a watchable pace, filmed by Playwright, cut to the tour and
# encoded as docs/atlas/hero.webp.
#
# Run it on a machine with a GPU (the recording does not use SwiftShader) after
# a visible Atlas change. The suite fails hero.spec.ts when a step of the tour
# no longer exists; this script only refreshes what the tour looks like.
#
# Usage: web/scripts/record-hero.sh   (make hero)
# Needs Node, ffmpeg, img2webp (brew install ffmpeg webp) and
# `npx playwright install chromium`.

set -euo pipefail

WEB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "${WEB_DIR}/.." && pwd)"
BIN="${REPO_ROOT}/bin/cartographer"
OUT="${REPO_ROOT}/docs/atlas/hero.webp"
WIDTH="${HERO_WIDTH:-960}"
FPS="${HERO_FPS:-12}"
QUALITY="${HERO_QUALITY:-60}"
# The tour is paced for a person driving it; played back faster it reads as a
# demo rather than a screen recording.
SPEED="${HERO_SPEED:-1.3}"

for tool in ffmpeg img2webp; do
    command -v "$tool" >/dev/null || { echo "record-hero: $tool is required (brew install ffmpeg webp)" >&2; exit 1; }
done

TMP="$(mktemp -d "${TMPDIR:-/tmp}/cartographer_hero_XXXXXX")"
PID=""
cleanup() {
    if [ -n "$PID" ]; then kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; fi
    rm -rf "$TMP"
}
trap cleanup EXIT

make -C "$REPO_ROOT" --no-print-directory build
node "${WEB_DIR}/scripts/demo-kb.mjs" "$TMP" >/dev/null

PORT="$(node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')"
CARTOGRAPHER_AUTH=false "$BIN" serve --init --http "127.0.0.1:${PORT}" --kb "${TMP}/demo" >"${TMP}/serve.log" 2>&1 &
PID="$!"
URL="http://127.0.0.1:${PORT}"
for _ in $(seq 1 60); do
    curl -sf "${URL}/health" 2>/dev/null | grep -q '"kbs"' && break
    sleep 0.5
done

cd "$WEB_DIR"
E2E_LOCAL_URL="$URL" E2E_AUTH_URL="$URL" HERO_RECORD=1 \
    HERO_KB=demo HERO_CONCEPT=astronomy/orbit HERO_QUERY=orbit HERO_ARTIFACT=star-chart \
    npx playwright test hero.spec.ts --output "${TMP}/run"

VIDEO="$(find "${TMP}/run" -name '*.webm' | head -1)"
START="$(cat "$(find "${TMP}/run" -name tour-start.txt | head -1)")"

# Animated WebP, not GIF: the living graph changes most pixels of every frame,
# which a 256-colour GIF pays for at about three times the size (D330).
mkdir -p "${TMP}/frames"
ffmpeg -v error -ss "$START" -i "$VIDEO" -vf "setpts=PTS/${SPEED},fps=${FPS},scale=${WIDTH}:-1:flags=lanczos" "${TMP}/frames/%04d.png"
img2webp -loop 0 -lossy -q "$QUALITY" -m 4 -d $((1000 / FPS)) "${TMP}"/frames/*.png -o "$OUT" >/dev/null

echo "record-hero: $(du -h "$OUT" | cut -f1) -> ${OUT#"${REPO_ROOT}/"}"
