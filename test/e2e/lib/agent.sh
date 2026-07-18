#!/usr/bin/env bash
# lib/agent.sh — operator function to invoke OpenCode in headless mode.
# The agent receives the mandate via --dangerously-skip-permissions and works only via MCP.

# Path to the OpenCode executable (overridable via env).
OPENCODE_BIN="${OPENCODE_BIN:-/opt/homebrew/bin/opencode}"

# agent_run <sandbox_dir> <mandate>
#   Runs opencode in headless mode in the given sandbox.
#   Captures stdout in <sandbox_dir>/agent.log, stderr in <sandbox_dir>/agent.err.
#   Returns opencode's exit code.
agent_run() {
    local sandbox_dir="$1"
    local mandate="$2"

    if [[ ! -d "$sandbox_dir" ]]; then
        echo "[ERROR] agent_run: sandbox not found: ${sandbox_dir}" >&2
        return 1
    fi

    if [[ ! -x "$OPENCODE_BIN" ]]; then
        echo "[ERROR] agent_run: opencode not found: ${OPENCODE_BIN}" >&2
        return 1
    fi

    if [[ -z "${E2E_MODEL:-}" ]]; then
        echo "[ERROR] agent_run: E2E_MODEL not set" >&2
        return 1
    fi

    local log_file="${sandbox_dir}/agent.log"
    local err_file="${sandbox_dir}/agent.err"

    echo "[agent] starting opencode in ${sandbox_dir}" >&2
    echo "[agent] model: ${E2E_MODEL}" >&2
    echo "[agent] mandate: ${mandate}" >&2

    "$OPENCODE_BIN" run \
        --dir "$sandbox_dir" \
        -m "$E2E_MODEL" \
        --dangerously-skip-permissions "$mandate" \
        >"$log_file" 2>"$err_file"

    local rc=$?
    echo "[agent] opencode finished with exit code ${rc}" >&2
    echo "[agent] log: ${log_file}" >&2
    return $rc
}
