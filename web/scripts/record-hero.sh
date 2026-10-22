#!/usr/bin/env bash
# Records the README's hero animation (D330, D790): e2e/hero.spec.ts walked over
# the demo KB on a virtual clock, one screenshot per frame, and encoded as an
# animated WebP.
#
# Headless and without a GPU, so it runs on any machine and on a CI runner: the
# spec advances Playwright's clock by 1000/FPS ms and shoots, so a slow software
# renderer makes the capture take longer, not look choppier. The suite fails
# hero.spec.ts when a step of the tour no longer exists; this script only
# refreshes what the tour looks like, and refuses a capture whose page does not
# fill the frame.
#
# Usage: web/scripts/record-hero.sh   (make hero)
#   HERO_OUT      where the WebP goes (default <repo>/hero.webp, gitignored)
#   HERO_WIDTH    encoded width in px (default 1600)
#   HERO_FPS      frames per second of the film (default 30)
#   HERO_QUALITY  WebP quality (default 85: ~17 MB; 92 is ~25 MB, too heavy for a README)
#   HERO_KEEP     keep the temporary directory (frames, server log)
# Needs Node, ffmpeg, img2webp (brew install ffmpeg webp / apt install ffmpeg
# webp) and `npx playwright install chromium`.

set -euo pipefail

WEB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "${WEB_DIR}/.." && pwd)"
BIN="${REPO_ROOT}/bin/cartographer"
OUT="${HERO_OUT:-${REPO_ROOT}/hero.webp}"
WIDTH="${HERO_WIDTH:-1600}"
FPS="${HERO_FPS:-30}"
QUALITY="${HERO_QUALITY:-85}"

for tool in ffmpeg ffprobe img2webp; do
    command -v "$tool" >/dev/null || { echo "record-hero: $tool is required (brew install ffmpeg webp / apt install ffmpeg webp)" >&2; exit 1; }
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
# serve never creates a KB (D906): the fixture folder becomes one.
"${REPO_ROOT}/test/lib/kb-bootstrap.sh" "${TMP}/demo"

PORT="$(node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')"
CARTOGRAPHER_AUTH=false "$BIN" serve --http "127.0.0.1:${PORT}" --kb "${TMP}/demo" >"${TMP}/serve.log" 2>&1 &
PID="$!"
URL="http://127.0.0.1:${PORT}"
for _ in $(seq 1 60); do
    curl -sf "${URL}/health" 2>/dev/null | grep -q '"started_at"' && break
    sleep 0.5
done

cd "$WEB_DIR"
STARTED="$(date +%s)"
E2E_LOCAL_URL="$URL" E2E_AUTH_URL="$URL" HERO_RECORD=1 HERO_FPS="$FPS" \
    HERO_KB=demo HERO_CONCEPT=astronomy/orbit HERO_QUERY=orbit HERO_ARTIFACT=star-chart \
    npx playwright test hero.spec.ts --output "${TMP}/run"
CAPTURED=$(($(date +%s) - STARTED))

FRAMES="$(find "${TMP}/run" -type d -name frames | head -1)"
COUNT="$(find "$FRAMES" -name '*.png' | wc -l | tr -d ' ')"
[ "$COUNT" -gt 0 ] || { echo "record-hero: the tour wrote no frames" >&2; exit 1; }
# The tuning number for CI (the hero workflow's timeout): what a frame costs here.
LC_ALL=C awk -v n="$COUNT" -v t="$CAPTURED" -v fps="$FPS" 'BEGIN {
    printf "record-hero: captured %d frames (%.1fs of film at %d fps) in %ds, %.2fs a frame\n", n, n / fps, fps, t, t / n
}'

# Animated WebP, not GIF: the living graph changes most pixels of every frame,
# which a 256-colour GIF pays for at about three times the size (D330).
# The frames are 2x screenshots of the viewport; ffmpeg scales them down.
mkdir -p "${TMP}/out"
ffmpeg -v error -framerate "$FPS" -i "${FRAMES}/%05d.png" -vf "scale=${WIDTH}:-1:flags=lanczos" "${TMP}/out/%04d.png"

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
mkdir -p "$(dirname "$OUT")"
img2webp -loop 0 -lossy -q "$QUALITY" -m 4 -d $((1000 / FPS)) "${TMP}"/out/*.png -o "$OUT" >/dev/null

echo "record-hero: $(du -h "$OUT" | cut -f1) -> ${OUT#"${REPO_ROOT}/"}"
