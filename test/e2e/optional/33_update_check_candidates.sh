#!/usr/bin/env bash
# optional/33_update_check_candidates.sh — `update check` never offers a release candidate
# (D1192), checked against a local fake of the GitHub release list.
#
#   1. Only candidates newer than the build exist: nothing is available, the newest stable
#      (older) is the "latest", `update notice` prints nothing.
#   2. A newer stable release sits beside a still newer candidate: the stable one is offered,
#      never the candidate.
#
# Optional: not in `make e2e`. The binary under test is built here with a release version,
# because a `dev` build never checks. CARTOGRAPHER_UPDATE_API_URL exists for this (tests only).
#
# Expected environment variables: E2E_TMP_DIR, E2E_HTTP_PORT, REPO_ROOT.

set -uo pipefail

SCENARIO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(cd "${SCENARIO_DIR}/.." && pwd)"

# shellcheck source=../lib/assert.sh
source "${E2E_DIR}/lib/assert.sh"
# shellcheck source=../lib/server.sh
source "${E2E_DIR}/lib/server.sh"

SCENARIO_NAME="33_update_check_candidates"
echo "=== Scenario ${SCENARIO_NAME} ==="

command -v python3 >/dev/null 2>&1 || { echo "[ERROR] ${SCENARIO_NAME} needs python3 to serve the fake release list" >&2; exit 1; }

DIR="${E2E_TMP_DIR}/${SCENARIO_NAME}"
FAKE="${DIR}/fake"
BIN="${DIR}/cartographer"
mkdir -p "$FAKE" "${DIR}/home"
(cd "$REPO_ROOT" && go build -ldflags "-X main.version=v1.0.0" -o "$BIN" ./cmd/cartographer) || { echo "[ERROR] build failed"; exit 1; }

PORT="$(e2e_free_port)" || exit 1
(cd "$FAKE" && exec python3 -m http.server "$PORT" --bind 127.0.0.1 >"${DIR}/fake.log" 2>&1) &
FAKE_PID=$!
trap 'kill "$FAKE_PID" 2>/dev/null || true' EXIT
for _ in $(seq 1 40); do e2e_port_busy "$PORT" && break; sleep 0.25; done
if ! kill -0 "$FAKE_PID" 2>/dev/null || ! e2e_port_busy "$PORT"; then
    echo "[ERROR] ${SCENARIO_NAME}: the fake release list did not start on 127.0.0.1:${PORT}; log:" >&2
    cat "${DIR}/fake.log" >&2 || true
    exit 1
fi

# Every command runs with its own home and cache: the real cache must not answer.
run() {
    HOME="${DIR}/home" XDG_CONFIG_HOME="${DIR}/home/.config" XDG_CACHE_HOME="${DIR}/home/.cache" \
    CARTOGRAPHER_UPDATE_API_URL="http://127.0.0.1:${PORT}" CARTOGRAPHER_NO_UPDATE_CHECK= GITHUB_TOKEN= \
        "$BIN" "$@"
}

echo ""
echo "--- Phase 1: only candidates newer than the build ---"
printf '[{"tag_name":"v1.2.0-rc.20990101","draft":false,"prerelease":true},{"tag_name":"v0.9.0","draft":false,"prerelease":true}]' > "${FAKE}/releases"
OUT="$(run update check --output json 2>&1)"
assert_mcp_contains "the build is v1.0.0" "$OUT" '"current":"v1.0.0"'
assert_mcp_contains "the newest stable release is the latest" "$OUT" '"latest":"v0.9.0"'
assert_mcp_contains "nothing is available" "$OUT" '"available":false'
assert_mcp_not_discloses "the candidate is not offered" "$OUT" "rc."
OUT="$(run update notice 2>&1)"
if [[ -z "$OUT" ]]; then _assert_pass "update notice prints nothing"; else _assert_fail "update notice printed: ${OUT:0:200}"; fi

echo ""
echo "--- Phase 2: a newer stable release beside a newer candidate ---"
printf '[{"tag_name":"v1.2.0-rc.20990101","draft":false,"prerelease":true},{"tag_name":"v1.0.1","draft":false,"prerelease":true}]' > "${FAKE}/releases"
OUT="$(run update check --output json 2>&1)"
assert_mcp_contains "the stable release is offered" "$OUT" '"latest":"v1.0.1"'
assert_mcp_contains "it is available" "$OUT" '"available":true'
assert_mcp_not_discloses "the candidate is not offered" "$OUT" "rc."
OUT="$(run update notice 2>&1)"
assert_mcp_contains "the notice names the stable release" "$OUT" "v1.0.1"
assert_mcp_not_discloses "the notice never names the candidate" "$OUT" "rc."

if [[ "$E2E_FAILURES" -eq 0 ]]; then
    echo "[SCENARIO ${SCENARIO_NAME}] PASS"
    exit 0
else
    echo "[SCENARIO ${SCENARIO_NAME}] FAIL (${E2E_FAILURES} assertion(s) failed)"
    exit 1
fi
