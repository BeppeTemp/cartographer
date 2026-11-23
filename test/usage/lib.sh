#!/usr/bin/env bash
# lib.sh — what test/usage/run.sh and the task checks share. Source it; it sets nothing by itself.
#
# The binary under test and the KB come from the environment the harness exports: USAGE_BIN, USAGE_KB
# and USAGE_ISO, the isolation of the binary as a newline-separated `NAME=value` list (HOME and the four
# XDG directories under the scratch directory), so a check never touches the real HOME.

# usage_env_run <cmd> [args...]: the command under the isolation (`env -i`: a CARTOGRAPHER_* or a
# token variable of the shell is gone, and would override the tokens of a file).
usage_env_run() {
  # USAGE_ISO is one assignment per line: split on newlines only, no globbing (a path may hold a space).
  (
    IFS=$'\n'
    set -f
    # shellcheck disable=SC2086
    exec env -i $USAGE_ISO "$@"
  )
}

# usage_call <tool> <arguments-json>: one tool call on the KB over stdio, in its own server session.
# Prints the text of the result; exits 1 when the tool answered an error. The server answers on
# its own thread and exits at the end of its input, so the input stays open until the answer is in.
usage_call() {
  local out i=0 reply
  out=$(mktemp "${USAGE_SCRATCH:-${TMPDIR:-/tmp}}/call.XXXXXX")
  {
    printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"usage-test","version":"0"}}}' \
      '{"jsonrpc":"2.0","method":"notifications/initialized"}'
    jq -nc --arg t "$1" --argjson a "$2" '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:$t,arguments:$a}}'
    while ! grep -q '"id":2,' "$out" && [ "$i" -lt 100 ]; do sleep 0.1; i=$((i + 1)); done
  } | usage_env_run "$USAGE_BIN" serve --kb "$USAGE_KB" >"$out" 2>/dev/null
  reply=$(jq -c 'select(.id == 2)' "$out")
  rm -f "$out"
  [ -n "$reply" ] || { echo "no answer to $1"; return 1; }
  jq -r '(.result.content[0].text // .error.message // "")' <<<"$reply"
  [ "$(jq -r '.result.isError // false' <<<"$reply")" = false ] && [ "$(jq -r 'has("error")' <<<"$reply")" = false ]
}

# usage_lint_clean: fails, saying what, unless the KB's lint has no finding.
usage_lint_clean() {
  local text n
  text=$(usage_call lint '{}') || { echo "lint failed: $text"; return 1; }
  n=$(jq -r '.findings_after_filter // 0' <<<"$text")
  [ "$n" = 0 ] || { echo "the KB's lint has $n finding(s): $(jq -c '[.findings[]?.check] | unique' <<<"$text")"; return 1; }
}

# usage_commits: the number of commits of the KB.
usage_commits() { git -C "$USAGE_KB" rev-list --count HEAD; }
