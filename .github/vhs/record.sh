#!/usr/bin/env bash
# Records docs/assets/demo.gif from .github/vhs/demo.tape.
#
# Re-run whenever the CLI/TUI UX changes visibly. Requires vhs
# (`brew install vhs`). Everything runs against a throwaway server and an
# isolated HOME: nothing on the real machine is touched.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

command -v vhs >/dev/null || { echo "error: vhs not installed (brew install vhs)" >&2; exit 1; }

make build >/dev/null

# Fixed, readable path: the TUI header shows $HOME, and a mktemp path would
# look noisy in the published GIF.
DEMO_DIR="/tmp/cartographer-demo"
DEMO_HOME="${DEMO_DIR}/home"
rm -rf "$DEMO_DIR"
mkdir -p "$DEMO_HOME" docs/assets

# Throwaway server: TWO demo KBs, HTTP on :8080, no auth. Two is the minimum
# that exercises what the dashboard actually learned to say — the server panel's
# KB line and the per-provider binding line have nothing to show with one.
CARTOGRAPHER_AUTH=false ./bin/cartographer serve \
    --kb "${DEMO_DIR}/homelab-kb,${DEMO_DIR}/projects-kb" --init --http :8080 &
SERVER_PID=$!
cleanup() {
    kill "$SERVER_PID" 2>/dev/null || true
    rm -rf "$DEMO_DIR"
}
trap cleanup EXIT

for _ in $(seq 1 40); do
    curl -fsS http://127.0.0.1:8080/health >/dev/null 2>&1 && break
    sleep 0.25
done
curl -fsS http://127.0.0.1:8080/health >/dev/null || { echo "error: demo server not healthy" >&2; exit 1; }

# Isolated HOME: the client writes machine-wide ($HOME), the demo must not
# touch the real configuration. PATH gets the freshly built binary.
HOME="$DEMO_HOME" PATH="${REPO_ROOT}/bin:${PATH}" vhs .github/vhs/demo.tape

# Size pass. The GIF loads at the top of the README on every visit, so it is
# worth a third of its bytes. A terminal recording uses a handful of colours, so
# quantising to 64 is visually lossless here — verified frame by frame against
# the unoptimised recording. Optional on purpose: vhs alone still produces a
# correct GIF, just a larger one, and the warning says so rather than letting
# the next person wonder why their diff is 100 KB bigger.
if command -v ffmpeg >/dev/null; then
    ffmpeg -v error -i docs/assets/demo.gif \
        -vf "fps=20,split[s0][s1];[s0]palettegen=max_colors=64[p];[s1][p]paletteuse=dither=bayer:bayer_scale=5" \
        -y "${DEMO_DIR}/demo-optimised.gif"
    mv "${DEMO_DIR}/demo-optimised.gif" docs/assets/demo.gif
else
    echo "warning: ffmpeg not found — the GIF is unoptimised and noticeably larger than the committed one" >&2
fi

echo "recorded: docs/assets/demo.gif ($(wc -c < docs/assets/demo.gif | tr -d ' ') bytes)"
