#!/usr/bin/env bash
# Browser-level verification of the embedded Atlas UI (D228).
#
# The suite runs against bin/cartographer serving the bundle embedded in it —
# never against Vite's dev server. The dev server serves different bytes under
# different headers (no CSP, no SPA fallback, no auth chain, modules instead of
# the hashed production assets): a test that passes there verifies nothing that
# ships. Node runs the test driver only; the server under test has none.
#
# Three servers, all on ephemeral loopback ports, each over its own copy of the
# fixture (e2e/fixture.mjs):
#   local — auth off, the out-of-the-box single-user experience;
#   auth  — auth on, with an admin token and one narrowed to Entity concepts
#           under infra/ in the atlas KB;
#   ext   — auth on, with a pinned plugin whose atlas side declares a view and
#           an app (D1938); the app lives on a fourth loopback port, served by
#           e2e/app-origin.mjs, which records the headers it receives.
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
        # Past bootstrap too (D348): until then /ui/ and /api are 503.
        local body
        body=$(curl -sf "${url}/health" 2>/dev/null) || body=""
        if printf '%s' "$body" | grep -q '"started_at"' && ! printf '%s' "$body" | grep -q '"bootstrapping"'; then return 0; fi
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
# serve never creates a KB (D906): the fixture folders become ones.
"${REPO_ROOT}/test/lib/kb-bootstrap.sh" "${TMP}"/local/{atlas,annex,void} "${TMP}"/auth/{atlas,annex,void}

LOCAL_PORT="$(free_port)"
AUTH_PORT="$(free_port)"

# Each server keeps its credentials and its audit log (always on, D905) in its
# own temp directory, not in the runner's home.
CARTOGRAPHER_CREDENTIALS_DIR="${TMP}/local-credentials" CARTOGRAPHER_AUDIT_LOG="${TMP}/local-audit/audit.log" \
CARTOGRAPHER_AUTH=false "$BIN" serve --http "127.0.0.1:${LOCAL_PORT}" \
    --kb "${TMP}/local/atlas,${TMP}/local/annex,${TMP}/local/void" \
    >"${TMP}/local.log" 2>&1 &
PIDS+=("$!")

cat >"${TMP}/auth.yaml" <<EOF
http: "127.0.0.1:${AUTH_PORT}"
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
EOF
# The tokens come from the YAML alone: an operator's exported env must not
# widen or replace them.
# The KBs are folders, mounted from the command line: a kbs[] entry names a remote.
env -u CARTOGRAPHER_TOKENS -u CARTOGRAPHER_AUTH \
    CARTOGRAPHER_CREDENTIALS_DIR="${TMP}/auth-credentials" CARTOGRAPHER_AUDIT_LOG="${TMP}/auth-audit/audit.log" \
    "$BIN" serve --config "${TMP}/auth.yaml" \
    --kb "${TMP}/auth/atlas,${TMP}/auth/annex,${TMP}/auth/void" \
    >"${TMP}/auth.log" 2>&1 &
PIDS+=("$!")

# ext: the same code, built as a dev build. A schema 2 plugin declares a floor
# (>=0.29.0) that `git describe` of a checkout whose last tag is older, or a
# shallow one, would make the pin refuse. A "dev" version satisfies every range
# (core.RangeMet), so the floor never depends on the checkout, and it is on no
# release line: the server asks GitHub for neither an update nor the release
# history (serverUpdateEnabled), which a suite that runs offline must not do.
# The bundle it serves is the same one.
EXT_BIN="${TMP}/cartographer-ext"
(cd "$REPO_ROOT" && go build -ldflags "-X main.version=dev" -o "$EXT_BIN" ./cmd/cartographer)

node "${WEB_DIR}/e2e/fixture.mjs" "${TMP}/ext"
"${REPO_ROOT}/test/lib/kb-bootstrap.sh" "${TMP}"/ext/{atlas,annex,void}

EXT_PORT="$(free_port)"
APP_PORT="$(free_port)"

# The plugin is written here because its app URL holds the app's port. The view
# draws the public KB list (a stable GET route); the app opens /board/ on the
# app origin.
PLUGIN_SRC="${TMP}/ext-plugin"
mkdir -p "${PLUGIN_SRC}/views" "${PLUGIN_SRC}/apps"
cat >"${PLUGIN_SRC}/cartographer-plugin.yaml" <<EOF
schema: 2
name: e2e-board
version: 1.0.0
display_name: E2E board
description: A view over the KB list and an app on its own origin.
cartographer: ">=0.29.0"
components:
  atlas:
    sdk: {views: 1, apps: 1}
EOF
cat >"${PLUGIN_SRC}/views/registry.yaml" <<EOF
title: Registry
description: The KBs this server mounts.
icon: sources
scope: global
source:
  route: /api/v1/kbs
  items: kbs
renderer: table
columns:
  - {key: name, label: KB}
  - {key: status, label: Status, format: badge}
EOF
cat >"${PLUGIN_SRC}/apps/board.yaml" <<EOF
title: Board
description: A board hosted on another origin.
url: http://127.0.0.1:${APP_PORT}/board/
EOF

# The pin is the only consent a server takes: vendor the tree into a read-only
# store and pin the digest it prints.
if ! "$EXT_BIN" plugin vendor "$PLUGIN_SRC" --to "${TMP}/plugins" >"${TMP}/vendor.out" 2>&1; then
    echo "[e2e-web] plugin vendor failed:" >&2
    cat "${TMP}/vendor.out" >&2
    exit 1
fi
EXT_DIGEST="$(sed -n 's/^digest \([0-9a-f]\{64\}\)$/\1/p' "${TMP}/vendor.out")"
if [ -z "$EXT_DIGEST" ]; then
    echo "[e2e-web] plugin vendor printed no digest:" >&2
    cat "${TMP}/vendor.out" >&2
    exit 1
fi

# The public API (/api/v1) is off unless asked for, and the view reads it.
cat >"${TMP}/ext.yaml" <<EOF
http: "127.0.0.1:${EXT_PORT}"
api:
  enabled: true
auth:
  mode: "on"
  tokens:
    - token: e2e-ext-token
      id: e2e-ext
plugins_dir: "${TMP}/plugins"
plugins:
  e2e-board:
    digest: ${EXT_DIGEST}
    enabled: true
EOF
env -u CARTOGRAPHER_TOKENS -u CARTOGRAPHER_AUTH -u CARTOGRAPHER_API_ENABLED \
    CARTOGRAPHER_CREDENTIALS_DIR="${TMP}/ext-credentials" CARTOGRAPHER_AUDIT_LOG="${TMP}/ext-audit/audit.log" \
    "$EXT_BIN" serve --config "${TMP}/ext.yaml" \
    --kb "${TMP}/ext/atlas,${TMP}/ext/annex,${TMP}/ext/void" \
    >"${TMP}/ext.log" 2>&1 &
PIDS+=("$!")

node "${WEB_DIR}/e2e/app-origin.mjs" "$APP_PORT" >"${TMP}/app.log" 2>&1 &
PIDS+=("$!")

export E2E_LOCAL_URL="http://127.0.0.1:${LOCAL_PORT}"
export E2E_AUTH_URL="http://127.0.0.1:${AUTH_PORT}"
export E2E_EXT_URL="http://127.0.0.1:${EXT_PORT}"
export E2E_APP_URL="http://127.0.0.1:${APP_PORT}"
wait_health "$E2E_LOCAL_URL" "${TMP}/local.log"
wait_health "$E2E_AUTH_URL" "${TMP}/auth.log"
wait_health "$E2E_EXT_URL" "${TMP}/ext.log"
for _ in $(seq 1 60); do
    curl -sf "${E2E_APP_URL}/__requests" >/dev/null 2>&1 && break
    sleep 0.5
done
curl -sf "${E2E_APP_URL}/__requests" >/dev/null 2>&1 || {
    echo "[e2e-web] the app origin never answered; its log:" >&2
    cat "${TMP}/app.log" >&2
    exit 1
}

echo "[e2e-web] local ${E2E_LOCAL_URL}, auth ${E2E_AUTH_URL}, ext ${E2E_EXT_URL}, app ${E2E_APP_URL}"
cd "$WEB_DIR"
# A headless run talks to no display server. Handed one (WSLg exports DISPLAY
# and WAYLAND_DISPLAY), Chromium's GPU process stalls 70-100 s on it before its
# first WebGL context, so the first test's navigation never returns (#1665).
# A headed or UI run keeps them: it draws on that display.
case " $* " in
    *" --headed "*|*" --ui "*|*" --debug "*) ;;
    *) unset DISPLAY WAYLAND_DISPLAY ;;
esac
npx playwright test "$@"
