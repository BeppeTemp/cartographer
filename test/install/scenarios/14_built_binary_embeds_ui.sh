#!/bin/sh
# scenarios/14_built_binary_embeds_ui.sh — the Atlas UI travels inside the
# binary on every install path (D227).
#
# Every channel — Homebrew, the winget zip, install.sh, the container and
# `go install` — ships the output of one `go build ./cmd/cartographer`, so this
# builds exactly that, with the release's CGO_ENABLED=0 and stripped ldflags,
# and proves two things about it:
#
#   1. the committed bundle is in the executable: the provenance manifest's
#      source hash and the shell's hashed asset name are both inside it;
#   2. it serves the UI with no Node anywhere: the server runs with a PATH that
#      holds git and nothing else, and /ui/, a hashed asset and a deep client
#      route all answer.
#
# A bundle that is ignored by git, or excluded from a build context, fails (1)
# on the release machine long before anyone opens a browser.

SCENARIO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_TEST_DIR=$(CDPATH= cd -- "${SCENARIO_DIR}/.." && pwd)
REPO_ROOT=$(CDPATH= cd -- "${INSTALL_TEST_DIR}/../.." && pwd)
INSTALL_LIB_DIR="${INSTALL_TEST_DIR}/lib"

# shellcheck source=../lib/assert.sh
. "${INSTALL_LIB_DIR}/assert.sh"

SCENARIO_NAME="14_built_binary_embeds_ui"
echo "=== Scenario ${SCENARIO_NAME} ==="

TMP=$(mktemp -d)
SERVER_PID=""
cleanup() {
    [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null
    rm -rf "$TMP"
}
trap cleanup EXIT

DIST="${REPO_ROOT}/internal/webui/dist"
BIN="${TMP}/cartographer"

if ! (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=vtest" -o "$BIN" ./cmd/cartographer); then
    _assert_fail "go build ./cmd/cartographer failed"
    exit 1
fi
_assert_pass "the release build command produces a binary"

# --- 1. The bundle is inside the executable ---------------------------------
SOURCE_HASH=$(sed -n 's/.*"source_hash": *"\([0-9a-f]*\)".*/\1/p' "${DIST}/provenance.json")
if [ -z "$SOURCE_HASH" ]; then
    _assert_fail "internal/webui/dist/provenance.json carries no source_hash"
elif grep -aqF "$SOURCE_HASH" "$BIN"; then
    _assert_pass "the binary embeds the committed bundle's provenance (${SOURCE_HASH%"${SOURCE_HASH#????????????}"}…)"
else
    _assert_fail "the binary does not contain the provenance hash ${SOURCE_HASH}"
fi
ASSET=$(sed -n 's#.*src="/ui/\(assets/[^"]*\.js\)".*#\1#p' "${DIST}/index.html")
if [ -n "$ASSET" ] && grep -aqF "$ASSET" "$BIN"; then
    _assert_pass "the binary embeds the shell that references ${ASSET}"
else
    _assert_fail "the binary does not embed the shell's script reference '${ASSET}'"
fi

# --- 2. It serves the UI with no Node on PATH -------------------------------
# A PATH holding git and nothing else: serve --init needs git for the KB, and
# must need nothing that a frontend toolchain would provide.
mkdir -p "${TMP}/path" "${TMP}/data/ui-kb"
GIT=$(command -v git)
ln -s "$GIT" "${TMP}/path/git"
if PATH="${TMP}/path" command -v node >/dev/null 2>&1; then
    _assert_fail "the restricted PATH still resolves node"
else
    _assert_pass "node is not resolvable on the server's PATH"
fi

PORT=$((20000 + $$ % 10000))
BASE="http://127.0.0.1:${PORT}"
# CARTOGRAPHER_AUTH=false: the machine may carry CARTOGRAPHER_TOKENS, which in
# "auto" mode would turn server auth on. /ui/ is public either way, but the
# scenario must not depend on the developer's environment.
env -i HOME="$TMP" PATH="${TMP}/path" CARTOGRAPHER_AUTH=false \
    "$BIN" serve --data="${TMP}/data" --init --http="127.0.0.1:${PORT}" >"${TMP}/server.log" 2>&1 &
SERVER_PID=$!
i=0
while [ $i -lt 40 ]; do
    curl -sf "${BASE}/health" >/dev/null 2>&1 && break
    i=$((i + 1))
    sleep 0.25
done

SHELL_HEADERS=$(curl -s -D - -o "${TMP}/shell.html" "${BASE}/ui/")
assert_contains "$SHELL_HEADERS" "200" "GET /ui/ answers 200"
assert_contains "$SHELL_HEADERS" "Content-Security-Policy" "the shell carries its CSP"
assert_file_contains "${TMP}/shell.html" '<div id="root">' "GET /ui/ returns the embedded shell"

ASSET_STATUS=$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/ui/${ASSET}")
assert_eq "$ASSET_STATUS" "200" "the hashed script ${ASSET} is served from the binary"

DEEP_STATUS=$(curl -s -o "${TMP}/deep.html" -w '%{http_code}' "${BASE}/ui/some/client/route")
assert_eq "$DEEP_STATUS" "200" "a client-side route below /ui/ serves the shell"

LOG=$(cat "${TMP}/server.log")
assert_contains "$LOG" "Atlas UI on http://127.0.0.1:${PORT}/ui/" "startup logs the local UI address"

echo ""
if [ "$INSTALL_FAILURES" -eq 0 ]; then
    echo "[${SCENARIO_NAME}] PASS"
    exit 0
fi
echo "[${SCENARIO_NAME}] FAIL (${INSTALL_FAILURES} assertion(s) failed)"
echo "--- server log ---"
echo "$LOG"
exit 1
