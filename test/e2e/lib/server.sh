#!/usr/bin/env bash
# lib/server.sh — operator functions to start/stop bin/cartographer in HTTP mode.
#
# Optional environment variables for server_start:
#   E2E_AUTH    "true" | "false" (default: false) — enables bearer token auth
#   E2E_TOKENS  token string (required if E2E_AUTH=true)
#   E2E_CONFIG  server YAML path; when set it replaces --kb/--http flags

# HTTP port for the E2E tests. High port unlikely to collide with common
# services on either the conventional HTTP port or Cartographer's local default.
E2E_HTTP_PORT="${E2E_HTTP_PORT:-47821}"

# e2e_port_busy <port>
#   0 when something already listens on 127.0.0.1:<port>.
e2e_port_busy() {
    (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

# e2e_free_port [port-to-avoid...]
#   Prints a loopback port nobody listens on right now. Call it right before the
#   bind: a port fixed or derived at the top of a scenario can be taken by the
#   time the server binds it (a leftover server, a neighbour, #2275). The window
#   that remains is closed by e2e_serve, which retries on a refused bind.
e2e_free_port() {
    local tries=0 port other skip
    while [[ $tries -lt 200 ]]; do
        port=$((20000 + (RANDOM * 32768 + RANDOM) % 40000))
        tries=$((tries + 1))
        skip=false
        for other in "$@"; do [[ "$other" == "$port" ]] && skip=true; done
        $skip && continue
        e2e_port_busy "$port" || { echo "$port"; return 0; }
    done
    echo "[ERROR] e2e_free_port: no free port after ${tries} tries" >&2
    return 1
}

# e2e_wait_health <port> <pid> [timeout_sec]
#   0 when cartographer's /health answers (past bootstrap, D348); 2 when the
#   process <pid> exited first (no point in waiting); 1 on timeout.
e2e_wait_health() {
    local port="$1" pid="$2" timeout="${3:-20}" elapsed=0 body
    while [[ $elapsed -lt $timeout ]]; do
        kill -0 "$pid" 2>/dev/null || return 2
        body=$(curl -sf "http://127.0.0.1:${port}/health" 2>/dev/null) || body=""
        if grep -q -- '"started_at"' <<<"$body" && ! grep -q -- '"bootstrapping"' <<<"$body"; then
            return 0
        fi
        sleep 1; elapsed=$((elapsed + 1))
    done
    return 1
}

# e2e_serve <log> <serve args without --http...>
#   Starts "$BIN serve <args> --http 127.0.0.1:<free port>" in the background on a
#   port free right now, and waits for /health. A refused bind ("address already
#   in use") is retried on a new port, up to 5 times. Sets E2E_SERVE_PID and
#   E2E_SERVE_PORT. On failure it names the port it tried and shows the log tail,
#   and returns 1. The caller's environment (CARTOGRAPHER_AUTH=...) reaches the server.
e2e_serve() {
    local log="$1"; shift
    local attempt=0 port avoid="" rc
    E2E_SERVE_PID=""; E2E_SERVE_PORT=""
    while [[ $attempt -lt 5 ]]; do
        attempt=$((attempt + 1))
        # shellcheck disable=SC2086
        port="$(e2e_free_port $avoid)" || return 1
        "$BIN" serve "$@" --http "127.0.0.1:${port}" >"$log" 2>&1 &
        E2E_SERVE_PID=$!
        E2E_SERVE_PORT="$port"
        e2e_wait_health "$port" "$E2E_SERVE_PID"; rc=$?
        [[ $rc -eq 0 ]] && return 0
        if [[ $rc -eq 2 ]] && grep -qi -e 'address already in use' -e 'cannot listen' "$log"; then
            echo "[e2e] 127.0.0.1:${port} was taken at bind (attempt ${attempt}), retrying on a new port" >&2
            avoid="$avoid $port"
            continue
        fi
        break
    done
    echo "[ERROR] server did not come up on 127.0.0.1:${E2E_SERVE_PORT} (attempt ${attempt}); log ${log}:" >&2
    tail -n 20 "$log" >&2 || true
    kill "$E2E_SERVE_PID" 2>/dev/null || true
    return 1
}

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

    # A port taken since run.sh chose it (a leftover server, a neighbour): move
    # to a free one, which the scenario reads back from E2E_HTTP_PORT (#2275).
    if e2e_port_busy "$E2E_HTTP_PORT"; then
        local busy="$E2E_HTTP_PORT"
        E2E_HTTP_PORT="$(e2e_free_port)" || return 1
        export E2E_HTTP_PORT
        echo "[server] 127.0.0.1:${busy} is in use, using ${E2E_HTTP_PORT}" >&2
    fi

	# Loopback: a server with auth off refuses any other address (D906). The KBs
	# are made before the start: serve never creates one.
	local p
	IFS=',' read -ra _kb_list <<< "$kb_paths"
	for p in ${_kb_list[@]+"${_kb_list[@]}"}; do
		[[ -n "$p" ]] && { kb_bootstrap "$p" || return 1; }
	done
	local args=(serve --kb "$kb_paths" --http "127.0.0.1:${E2E_HTTP_PORT}")
	if [[ -n "${E2E_CONFIG:-}" ]]; then
		# The KBs are folders mounted from the command line: a kbs[] entry of the
		# config names a remote (D903), and the config carries the rest.
		args=(serve --config "$E2E_CONFIG")
		if [[ -n "$kb_paths" ]]; then
			args+=(--kb "$kb_paths")
		fi
	fi

	# The server's own files stay in the run's temp directory, never in the
	# runner's home: the audit log is always on and audit.key is generated at
	# the first start (D905). E2E_CREDENTIALS_DIR and E2E_AUDIT_LOG let a
	# scenario put them where it inspects them.
	local own="${E2E_TMP_DIR:-/tmp}/${SCENARIO_NAME:-server}"
	CARTOGRAPHER_AUTH="${auth}" \
	CARTOGRAPHER_TOKENS="${tokens}" \
	CARTOGRAPHER_CREDENTIALS_DIR="${E2E_CREDENTIALS_DIR:-${own}/credentials}" \
	CARTOGRAPHER_AUDIT_LOG="${E2E_AUDIT_LOG:-${own}/audit/audit.log}" \
		"$bin" "${args[@]}" \
        >"${E2E_TMP_DIR:-/tmp}/cartographer_e2e.log" 2>&1 &

    echo "$!" > "$_SERVER_PID_FILE"
    echo "[server] started PID=$(cat "$_SERVER_PID_FILE") KB=${kb_paths} AUTH=${auth}" >&2
}

# server_wait_health [timeout_sec]
#   Polls GET /health until it responds with a CARTOGRAPHER body or the timeout expires.
#   Verifies that the response contains "started_at" (marker of cartographer's /health): this way
#   another service listening on the same port (e.g. Docker/Colima) does not cause false positives.
#   TRAP: not "kbs". With auth on, a tokenless /health carries no kbs (D1096), while
#   started_at is in the anonymous, identified and bootstrapping shapes alike.
server_wait_health() {
    local timeout="${1:-20}"
    local elapsed=0
    local url="http://127.0.0.1:${E2E_HTTP_PORT}/health"

    echo "[server] waiting for /health on ${url} (timeout ${timeout}s)..." >&2
    while [[ $elapsed -lt $timeout ]]; do
        # Past bootstrap too (D348): /health answers while the KBs are still
        # being mounted, and every other path is 503 until then.
        local body
        body=$(curl -sf "$url" 2>/dev/null) || body=""
        if grep -q -- '"started_at"' <<<"$body" && ! grep -q -- '"bootstrapping"' <<<"$body"; then
            echo "[server] /health OK (cartographer) after ${elapsed}s" >&2
            return 0
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done

    echo "[ERROR] server_wait_health: no answer on 127.0.0.1:${E2E_HTTP_PORT} after ${timeout}s" >&2
    tail -n 20 "${E2E_TMP_DIR:-/tmp}/cartographer_e2e.log" >&2 2>/dev/null || true
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
