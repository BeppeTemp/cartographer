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

# Throwaway server: demo KB, HTTP on :8080, no auth.
CARTOGRAPHER_AUTH=false ./bin/cartographer serve \
    --kb "${DEMO_DIR}/demo-kb" --init --http :8080 &
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

echo "recorded: docs/assets/demo.gif"
