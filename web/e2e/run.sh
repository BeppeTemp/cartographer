#!/usr/bin/env bash
# Browser-level verification of the embedded Atlas UI (D228).
#
# The suite runs against bin/cartographer serving the bundle embedded in it —
# never against Vite's dev server. The dev server serves different bytes under
# different headers (no CSP, no SPA fallback, no auth chain, modules instead of
# the hashed production assets): a test that passes there verifies nothing that
# ships. Node runs the test driver only; the server under test has none.
#
# Two servers, both on ephemeral loopback ports, each over its own copy of the
# fixture (e2e/fixture.mjs):
#   local — auth off, the out-of-the-box single-user experience;
#   auth  — auth on, with an admin token and one narrowed to Entity concepts
#           under infra/ in the atlas KB.
#
# Usage: web/e2e/run.sh [playwright args...]   (make e2e-web)

set -euo pipefail

WEB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "${WEB_DIR}/.." && pwd)"
BIN="${REPO_ROOT}/bin/cartographer"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/cartographer_e2e_web_XXXXXX")"
PIDS=()
cleanup() {
    for pid in "${PIDS[@]}"; do kill "$pid" 2>/dev/null || true; done
    for pid in "${PIDS[@]}"; do wait "$pid" 2>/dev/null || true; done
    rm -rf "$TMP"
}
trap cleanup EXIT

free_port() {
    node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})'
}

wait_health() {
    local url="$1" log="$2"
    for _ in $(seq 1 60); do
        if curl -sf "${url}/health" 2>/dev/null | grep -q '"kbs"'; then return 0; fi
        sleep 0.5
    done
    echo "[e2e-web] server at ${url} never became healthy; its log:" >&2
    cat "$log" >&2
    return 1
}

echo "[e2e-web] build"
make -C "$REPO_ROOT" --no-print-directory build

node "${WEB_DIR}/e2e/fixture.mjs" "${TMP}/local"
node "${WEB_DIR}/e2e/fixture.mjs" "${TMP}/auth"

LOCAL_PORT="$(free_port)"
AUTH_PORT="$(free_port)"

CARTOGRAPHER_AUTH=false "$BIN" serve --init --http "127.0.0.1:${LOCAL_PORT}" \
    --kb "${TMP}/local/atlas,${TMP}/local/annex,${TMP}/local/void" \
    >"${TMP}/local.log" 2>&1 &
PIDS+=("$!")

cat >"${TMP}/auth.yaml" <<EOF
http: "127.0.0.1:${AUTH_PORT}"
init: true
auth:
  mode: "on"
  roles:
    - name: infra-entities
      rules:
        - kb: atlas
          access: r
          maps: [infra]
          types: [Entity]
  tokens:
    - token: e2e-admin-token
      id: e2e-admin
    - token: e2e-narrow-token
      id: e2e-narrow
      roles: [infra-entities]
kbs:
  - path: ${TMP}/auth/atlas
  - path: ${TMP}/auth/annex
  - path: ${TMP}/auth/void
EOF
# The tokens come from the YAML alone: an operator's exported env must not
# widen or replace them.
env -u CARTOGRAPHER_TOKENS -u CARTOGRAPHER_AUTH "$BIN" serve --config "${TMP}/auth.yaml" \
    >"${TMP}/auth.log" 2>&1 &
PIDS+=("$!")

export E2E_LOCAL_URL="http://127.0.0.1:${LOCAL_PORT}"
export E2E_AUTH_URL="http://127.0.0.1:${AUTH_PORT}"
wait_health "$E2E_LOCAL_URL" "${TMP}/local.log"
wait_health "$E2E_AUTH_URL" "${TMP}/auth.log"

echo "[e2e-web] local ${E2E_LOCAL_URL}, auth ${E2E_AUTH_URL}"
cd "$WEB_DIR"
npx playwright test "$@"
