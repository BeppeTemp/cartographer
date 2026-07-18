#!/usr/bin/env bash
# lib/server.sh — operator functions to start/stop bin/cartographer in HTTP mode.
#
# Optional environment variables for server_start:
#   E2E_AUTH    "true" | "false" (default: false) — enables bearer token auth
#   E2E_TOKENS  token string (required if E2E_AUTH=true)

# HTTP port for the E2E tests. High port unlikely to collide with common
# services on 8080 (Docker/Colima, port-forward, etc.).
E2E_HTTP_PORT="${E2E_HTTP_PORT:-47821}"

# File where the server process PID is saved.
_SERVER_PID_FILE=""

# server_start <kb_paths_csv>
#   Starts bin/cartographer in the background with the given KBs (CSV of absolute paths).
#   Supports authentication via E2E_AUTH and E2E_TOKENS.
#   Saves the PID in _SERVER_PID_FILE for cleanup.
server_start() {
    local kb_paths="$1"
    local repo_root
    repo_root="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)"
    local bin="${repo_root}/bin/cartographer"

    if [[ ! -x "$bin" ]]; then
        echo "[ERROR] server_start: binary not found: $bin" >&2
        return 1
    fi

    # PID file in the run's temp directory (caller must set E2E_TMP_DIR).
    _SERVER_PID_FILE="${E2E_TMP_DIR:-/tmp}/cartographer_e2e.pid"

    local auth="${E2E_AUTH:-false}"
    local tokens="${E2E_TOKENS:-}"

    CARTOGRAPHER_AUTH="${auth}" \
    CARTOGRAPHER_TOKENS="${tokens}" \
        "$bin" serve \
        --kb "$kb_paths" \
        --init \
        --http ":${E2E_HTTP_PORT}" \
        >"${E2E_TMP_DIR:-/tmp}/cartographer_e2e.log" 2>&1 &

    echo "$!" > "$_SERVER_PID_FILE"
    echo "[server] started PID=$(cat "$_SERVER_PID_FILE") KB=${kb_paths} AUTH=${auth}" >&2
}

# server_wait_health [timeout_sec]
#   Polls GET /health until it responds with a CARTOGRAPHER body or the timeout expires.
#   Verifies that the response contains "kbs" (marker of cartographer's /health): this way
#   another service listening on the same port (e.g. Docker/Colima) does not cause false positives.
server_wait_health() {
    local timeout="${1:-20}"
    local elapsed=0
    local url="http://127.0.0.1:${E2E_HTTP_PORT}/health"

    echo "[server] waiting for /health on ${url} (timeout ${timeout}s)..." >&2
    while [[ $elapsed -lt $timeout ]]; do
        if curl -sf "$url" 2>/dev/null | grep -q '"kbs"'; then
            echo "[server] /health OK (cartographer) after ${elapsed}s" >&2
            return 0
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done

    echo "[ERROR] server_wait_health: timeout after ${timeout}s" >&2
    return 1
}

# server_stop
#   Terminates the server process saved in _SERVER_PID_FILE.
server_stop() {
    if [[ -z "$_SERVER_PID_FILE" || ! -f "$_SERVER_PID_FILE" ]]; then
        echo "[server] no PID to stop" >&2
        return 0
    fi

    local pid
    pid="$(cat "$_SERVER_PID_FILE")"
    if kill -0 "$pid" 2>/dev/null; then
        kill "$pid"
        echo "[server] process $pid terminated" >&2
    else
        echo "[server] process $pid already terminated" >&2
    fi

    rm -f "$_SERVER_PID_FILE"
}
