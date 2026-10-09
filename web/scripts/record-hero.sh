#!/usr/bin/env bash
# Records the README's hero animation (D330): e2e/hero.spec.ts walked over the
# demo KB at a watchable pace, filmed through the browser screencast and
# encoded as docs/atlas/hero.webp.
#
# Run it after a visible Atlas change, on a desktop session with a GPU and,
# for a sharp result, a Retina display: the recording opens a real Chromium
# window and films what it paints (2560x1520 on a 2x display). The suite fails
# hero.spec.ts when a step of the tour no longer exists; this script only
# refreshes what the tour looks like, and refuses a capture whose page does not
# fill the frame.
#
# Usage: web/scripts/record-hero.sh   (make hero)
# Needs Node, ffmpeg, img2webp (brew install ffmpeg webp) and
# `npx playwright install chromium`.

set -euo pipefail

WEB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "${WEB_DIR}/.." && pwd)"
BIN="${REPO_ROOT}/bin/cartographer"
OUT="${REPO_ROOT}/docs/atlas/hero.webp"
WIDTH="${HERO_WIDTH:-1600}"
FPS="${HERO_FPS:-30}"
QUALITY="${HERO_QUALITY:-92}"
# Playback speed; the beats in hero.spec.ts are already paced for a viewer.
SPEED="${HERO_SPEED:-1}"

for tool in ffmpeg img2webp; do
    command -v "$tool" >/dev/null || { echo "record-hero: $tool is required (brew install ffmpeg webp)" >&2; exit 1; }
done

TMP="$(mktemp -d "${TMPDIR:-/tmp}/cartographer_hero_XXXXXX")"
PID=""
cleanup() {
    if [ -n "$PID" ]; then kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; fi
    if [ -n "${HERO_KEEP:-}" ]; then echo "record-hero: kept ${TMP}"; else rm -rf "$TMP"; fi
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

FRAMES="$(find "${TMP}/run" -name frames.ffconcat | head -1)"
# The screencast delivers what the compositor paints, so its rate is the
# machine's, not FPS: printed so a choppy capture is visible before encoding.
LC_ALL=C awk '/^duration/ { n++; t += $2 } END { printf "record-hero: captured %d frames over %.1fs (%.0f/s)\n", n, t, n / t }' "$FRAMES"

# Animated WebP, not GIF: the living graph changes most pixels of every frame,
# which a 256-colour GIF pays for at about three times the size (D330).
# The screencast's frames carry their own timing (frames.ffconcat); ffmpeg
# resamples them to a constant rate, crops them to the page and scales the 2x
# capture down. A headed window's frame is the window surface, not the
# viewport: hero.spec.ts writes the page rectangle from the frame metadata
# (crop.txt, as fractions of the frame) and the crop comes before the scale.
CROP=""
if [ -f "$(dirname "$FRAMES")/crop.txt" ]; then
    read -r CW CH CX CY <"$(dirname "$FRAMES")/crop.txt"
    CROP="crop=trunc(iw*${CW}/2)*2:trunc(ih*${CH}/2)*2:trunc(iw*${CX}/2)*2:trunc(ih*${CY}/2)*2,"
fi
mkdir -p "${TMP}/out"
ffmpeg -v error -f concat -safe 0 -i "$FRAMES" \
    -vf "setpts=PTS/${SPEED},fps=${FPS},${CROP}scale=${WIDTH}:-1:flags=lanczos" "${TMP}/out/%04d.png"

# Guard: a bad capture fails here instead of reaching the README. The encoded
# aspect must be the viewport's (VIEW_W/VIEW_H, within 1%) and the first frames
# must have no dark border wider than a few pixels (cropdetect).
VIEW_W=1280
VIEW_H=760
FIRST="${TMP}/out/0001.png"
IFS=, read -r PW PH < <(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 "$FIRST")
LC_ALL=C awk -v w="$PW" -v h="$PH" -v vw="$VIEW_W" -v vh="$VIEW_H" 'BEGIN {
    d = (w / h) / (vw / vh) - 1; if (d < 0) d = -d
    if (d > 0.01) { printf "record-hero: encoded %dx%d (aspect %.3f) is not the viewport %dx%d (%.3f): the page does not fill the frame, nothing written\n", w, h, w / h, vw, vh, vw / vh > "/dev/stderr"; exit 1 }
}'
BORDER="$(ffmpeg -v info -i "${TMP}/out/%04d.png" -frames:v 30 -vf cropdetect=limit=10:round=2 -f null - 2>&1 \
    | grep -o 'crop=[0-9]*:[0-9]*:[0-9]*:[0-9]*' | tail -1 || true)"
if [ -n "$BORDER" ]; then
    IFS=: read -r BW BH BX BY <<<"${BORDER#crop=}"
    if [ $((PW - BW)) -gt 4 ] || [ $((PH - BH)) -gt 4 ]; then
        echo "record-hero: dark border in the first frames (${PW}x${PH}, content ${BORDER}): nothing written" >&2
        exit 1
    fi
fi
img2webp -loop 0 -lossy -q "$QUALITY" -m 4 -d $((1000 / FPS)) "${TMP}"/out/*.png -o "$OUT" >/dev/null

echo "record-hero: $(du -h "$OUT" | cut -f1) -> ${OUT#"${REPO_ROOT}/"}"
