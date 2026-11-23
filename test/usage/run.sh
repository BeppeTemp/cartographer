#!/usr/bin/env bash
# run.sh — the usage test: can an agent that has only the MCP server do a user's seven tasks on
# this binary? One headless agent run per task file, the verdict from assertions on the
# transcript and on the KB, never from a second model (docs/testing.md §Usage test).
#
#   make usage-test [BIN=<binary>] [TASKS=<names>] [MODEL=<id>] [KEEP=1]
#
# Run by a maintainer's coordinator in its own agent session, never by a workflow (a test of
# internal/repodocs fails when a file under .github/ names it): the agent CLI is the one installed
# and logged in on this machine, the harness installs nothing and holds no credential.
#
#   BIN               the binary under test (default bin/cartographer)
#   TASKS             comma-separated task names (`ingest_note` or `03_ingest_note`); default all
#   MODEL             the agent's model; default the one pinned in .claude/agents/cartographer-source-reader.md
#                     (the cheaper one; the larger is the pin of cartographer-contract-reviewer.md)
#   KEEP=1            keep the scratch directory (transcripts, KBs) after a pass
#   USAGE_AGENT_CMD   replaces the agent CLI (also USAGE_AGENT=<path>; a fake agent tests the harness
#                     with no network). By default the first `claude` on PATH that is a real
#                     executable, not a shell shim that wraps it
#   USAGE_TASKS_DIR   replaces test/usage/tasks (the harness's own test)
#   USAGE_BUDGET_USD  the spend cap of one agent run (--max-budget-usd, a number, default 2). The
#                     worst case of a whole run is (1 + tasks) x 2 attempts x this cap
#
# Exit: 0 every task passed; 1 a task or the isolation failed; 2 a wrong invocation (no jq or git, no
# binary, an unreadable budget, a task naming a model off the allowlist, no task matched); 75 the agent could not be started or
# the model was unavailable (no CLI, not logged in, a 5xx or a rate limit after one retry): the lane
# is NOT RUN, which says nothing about the binary.
#
# Every command of the binary runs under `env -i` with HOME and the four XDG directories under the
# scratch directory. The agent CLI keeps its own login, so it is the one process that touches the
# real HOME (its session files); the harness writes nowhere else than the scratch directory.
# Plain bash 3.2 (the macOS default); needs jq and git.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
BIN=${BIN:-$root/bin/cartographer}
case "$BIN" in /*) ;; *) BIN=$PWD/$BIN ;; esac
# The agent CLI: USAGE_AGENT_CMD or USAGE_AGENT=<path> when set. Otherwise the first `claude` on PATH
# whose first line is not a shell shebang (`#!/bin/sh`, `#!/usr/bin/env bash`, zsh...: a wrapper another
# tool puts first on PATH is not the CLI; a `#!/usr/bin/env node` file or a binary is). When every `claude`
# on PATH is a shell script, the first one is used, and `--version` in the report shows which.
AGENT=${USAGE_AGENT_CMD:-${USAGE_AGENT:-}}
if [ -z "$AGENT" ]; then
  AGENT=claude
  for c in $(type -aP claude 2>/dev/null); do
    first=$(head -n 1 "$c" 2>/dev/null | tr -d '\r')
    if ! grep -qE '^#![[:space:]]*(/usr/bin/env[[:space:]]+)?(/[^[:space:]]*/)?(ba|z|da|k|a)?sh([[:space:]]|$)' <<<"$first"; then AGENT=$c; break; fi
  done
  [ "$AGENT" != claude ] || AGENT=$(type -P claude 2>/dev/null || echo claude)
fi
TASKS_DIR=${USAGE_TASKS_DIR:-$here/tasks}
BUDGET=${USAGE_BUDGET_USD:-2}
read -r -a agentcmd <<<"$AGENT"

die() { echo "usage-test: $*" >&2; exit 2; }
[[ "$BUDGET" =~ ^[0-9]+(\.[0-9]+)?$ ]] || die "USAGE_BUDGET_USD must be a number of dollars, got '$BUDGET'"

rows=() fails=0 agentver=
# mdcell <text>: text from a transcript or a check made safe for one cell of the Markdown summary
# (no pipe, no tag, no line break, no backslash).
mdcell() {
  local r=$1
  r=${r//\\/\\\\}; r=${r//|/\\|}; r=${r//</"&lt;"}; r=${r//>/"&gt;"}; r=${r//$'\n'/ }
  printf '%s' "$r"
}
# The Markdown table of $GITHUB_STEP_SUMMARY.
summary_end() {
  [ -z "${GITHUB_STEP_SUMMARY:-}" ] && return 0
  printf '\n### Usage test (%s, %s)\n\n| Task | Verdict | Calls | Errors | Recoveries | Tokens in | Tokens out | Seconds |\n|---|---|---|---|---|---|---|---|\n' "$(mdcell "${MODEL:-?}")" "$(mdcell "${agentver:-?}")" >>"$GITHUB_STEP_SUMMARY"
  local r
  for r in ${rows[@]+"${rows[@]}"}; do printf '| %s |\n' "$r" >>"$GITHUB_STEP_SUMMARY"; done
  return 0
}
not_run() { echo "NOT RUN: $*"; summary_end; exit 75; }
command -v jq >/dev/null || die "jq is required"
command -v git >/dev/null || die "git is required"
[ -x "$BIN" ] || die "no binary at $BIN (make build, or BIN=<path>)"
command -v "${agentcmd[0]}" >/dev/null 2>&1 || not_run "the agent CLI '${agentcmd[0]}' is not on PATH"

# The model: the one pinned in the cheaper reader's definition, so that a bump there moves the lane.
if [ -z "${MODEL:-}" ]; then
  MODEL=$(awk -F': *' '/^model:/ { print $2; exit }' "$root/.claude/agents/cartographer-source-reader.md" | tr -d ' \r')
  [ -n "$MODEL" ] || die "no model pinned in .claude/agents/cartographer-source-reader.md"
fi

scratch=$(mktemp -d "${TMPDIR:-/tmp}/usage-test.XXXXXX") || die "no scratch directory"
mkdir -p "$scratch/home" "$scratch/xdg/config" "$scratch/xdg/state" "$scratch/xdg/data" "$scratch/xdg/cache" "$scratch/work"
safepath="$(dirname "$(command -v git)"):/usr/bin:/bin"
export USAGE_BIN=$BIN USAGE_SCRATCH=$scratch
# One assignment per line, so that a path with a space stays whole (lib.sh splits on newlines only).
USAGE_ISO="HOME=$scratch/home
XDG_CONFIG_HOME=$scratch/xdg/config
XDG_STATE_HOME=$scratch/xdg/state
XDG_DATA_HOME=$scratch/xdg/data
XDG_CACHE_HOME=$scratch/xdg/cache
PATH=$safepath"
export USAGE_ISO
# shellcheck source=lib.sh
. "$here/lib.sh"

agentver=$("${agentcmd[@]}" --version 2>&1 | head -n 1)
echo "usage-test: $BIN ($(usage_env_run "$BIN" version 2>&1 | head -n 1)); agent CLI: ${agentver:-unknown}; model: $MODEL"
echo "usage-test: scratch $scratch"

# make_kb <task-name> <seed>: a fresh KB with no remote under the scratch directory, seeded by the
# tool calls of fixtures/<seed>.jsonl through the binary itself.
make_kb() {
  local name=$1 seed=$2 dir=$scratch/kb-$1 line tool args
  mkdir -p "$dir"
  usage_env_run "$BIN" kb create kb --no-remote --local --data "$dir" >/dev/null 2>&1 || return 1
  USAGE_KB=$dir/kb
  export USAGE_KB
  if [ -n "$seed" ]; then
    [ -r "$here/fixtures/$seed.jsonl" ] || { echo "no fixture $seed"; return 1; }
    while IFS= read -r line || [ -n "$line" ]; do
      [ -n "$line" ] || continue
      tool=$(jq -r .tool <<<"$line")
      args=$(jq -c .arguments <<<"$line")
      usage_call "$tool" "$args" >/dev/null || { echo "seed call $tool failed"; return 1; }
    done <"$here/fixtures/$seed.jsonl"
  fi
  USAGE_SEED_COMMITS=$(git -C "$USAGE_KB" rev-list --count HEAD)
  export USAGE_SEED_COMMITS
}

# write_mcp_config <file>: the only MCP server of the agent is the binary serving USAGE_KB over stdio.
write_mcp_config() {
  (
    IFS=$'\n'
    set -f
    # shellcheck disable=SC2086
    jq -n --arg bin "$BIN" --arg kb "$USAGE_KB" --args '{mcpServers: {kb: {command: "/usr/bin/env", args: (["-i"] + $ARGS.positional + [$bin, "serve", "--kb", $kb])}}}' $USAGE_ISO >"$1"
  )
}

# run_agent <label> <prompt> <config> <transcript> <turn-ceiling>: one headless run, the transcript as
# stream-json, in an empty directory with only the MCP server. Retried once when it ends with
# no result or an error that reads as the service being unavailable. Returns 0 when a verdict can
# be drawn, 75 when the agent or the model is unavailable.
run_agent() {
  local label=$1 prompt=$2 cfg=$3 out=$4 maxturns=$5 attempt=0 res text
  while [ "$attempt" -lt 2 ]; do
    attempt=$((attempt + 1))
    (cd "$scratch/work" && USAGE_TASK=$label "${agentcmd[@]}" -p "$prompt" --output-format stream-json --verbose --include-hook-events \
      --mcp-config "$cfg" --strict-mcp-config --setting-sources local --disable-slash-commands --no-session-persistence \
      --tools "" --allowedTools mcp__kb --model "$USAGE_MODEL" --max-turns "$maxturns" --max-budget-usd "$BUDGET") >"$out" 2>"$out.err"
    res=$(jq -c 'select(.type == "result")' "$out" 2>/dev/null | tail -n 1)
    if [ -z "$res" ]; then continue; fi
    if [ "$(jq -r '.is_error // false' <<<"$res")" = true ]; then
      text=$(jq -r '.result // ""' <<<"$res")
      if grep -qiE 'log ?in|credit|rate.?limit|overloaded|unavailable|5[0-9][0-9]|not.found|api error|authenticat' <<<"$text"; then continue; fi
    fi
    return 0
  done
  return 75
}

# init_of <transcript>: the session's first event.
init_of() { jq -c 'select(.type == "system" and .subtype == "init")' "$1" 2>/dev/null | head -n 1; }

# The models a task file may name: the pins of the cheaper reader and of the larger reviewer.
allowed=$(awk -F': *' '/^model:/ { print $2 }' "$root/.claude/agents/cartographer-source-reader.md" "$root/.claude/agents/cartographer-contract-reviewer.md" | tr -d ' \r')
# Step 0, isolation: one empty run with only the MCP config. The session must list that server
# and no other, no tool but its own, no skill or slash command (--disable-slash-commands leaves none:
# one reported fails the run), no plugin but the CLI's built-in ones and no hook on an event other than
# SessionStart. The run calls one allowed tool, so that a PreToolUse hook would fire and be reported
# (--include-hook-events). Before a task is paid for.
USAGE_MODEL=$MODEL
export USAGE_MODEL
make_kb isolation "" || not_run "cannot make a KB with $BIN"
write_mcp_config "$scratch/mcp.json"
run_agent isolation "Check the knowledge base for problems once, then reply with the single word ready." "$scratch/mcp.json" "$scratch/isolation.jsonl" 6
rc=$?
if [ "$rc" = 75 ]; then
  echo "agent stderr: $(head -c 300 "$scratch/isolation.jsonl.err" 2>/dev/null)"
  not_run "the agent could not run or the model is unavailable (isolation run; transcript $scratch/isolation.jsonl)"
fi
init=$(init_of "$scratch/isolation.jsonl")
why=
if [ -z "$init" ]; then why="the transcript has no init event"
else
  names=$(jq -r '[.mcp_servers[]?.name] | join(",")' <<<"$init")
  [ "$names" = kb ] || why="the session lists MCP servers '$names', not only kb"
  [ -n "$why" ] || { st=$(jq -r '[.mcp_servers[]? | select(.status != "connected")] | length' <<<"$init"); [ "$st" = 0 ] || why="the kb server did not connect (the binary did not start as an MCP server)"; }
  [ -n "$why" ] || { other=$(jq -r '[.tools[]? | select((startswith("mcp__kb__") | not))] | join(",")' <<<"$init"); [ -z "$other" ] || why="the session has tools besides the server's: $other"; }
  [ -n "$why" ] || { pl=$(jq -r '[.plugins[]? | select(.path != "builtin") | .name] | join(",")' <<<"$init"); [ -z "$pl" ] || why="the session loaded plugins: $pl"; }
  # --disable-slash-commands leaves no skill and no command: one reported is the user's.
  [ -n "$why" ] || { sk=$(jq -r '[(.skills // [])[], (.slash_commands // [])[]] | join(",")' <<<"$init"); [ -z "$sk" ] || why="the session lists skills or commands: $sk"; }
  # The CLI's own plugins run a SessionStart hook; a hook on another event is the user's.
  [ -n "$why" ] || { hk=$(jq -r 'select(.type == "system" and .subtype == "hook_started" and .hook_event != "SessionStart") | .hook_name' "$scratch/isolation.jsonl" | sort -u | tr '\n' ','); [ -z "$hk" ] || why="the session ran hooks: ${hk%,}"; }
fi
if [ -n "$why" ]; then
  echo "FAIL isolation: $why (transcript $scratch/isolation.jsonl)"
  summary_end
  exit 1
fi
echo "PASS isolation: only the kb MCP server ($(jq -r '[.tools[]?] | length' <<<"$init") tools), no foreign plugin"

# Tasks.
selected() { # <task file base name>: is it in TASKS?
  local b=$1 n w
  [ -n "${TASKS:-}" ] || return 0
  n=${b#*_}
  for w in $(tr ',' ' ' <<<"$TASKS"); do [ "$w" = "$b" ] || [ "$w" = "$n" ] && return 0; done
  return 1
}
header() { awk -v k="$2" 'NR == 1 && $0 != "---" { exit } NR > 1 && $0 == "---" { exit } NR > 1 { i = index($0, ":"); if (substr($0, 1, i - 1) == k) { v = substr($0, i + 1); gsub(/^[ \t]+|[ \t\r]+$/, "", v); print v; exit } }' "$1"; }
body() { awk 'NR == 1 && $0 == "---" { h = 1; next } h && $0 == "---" { h = 0; next } !h { print }' "$1"; }

ran=0
for f in "$TASKS_DIR"/[0-9][0-9]_*.md; do
  [ -e "$f" ] || break
  base=$(basename "$f" .md)
  selected "$base" || continue
  ran=$((ran + 1))
  turns=$(header "$f" turns); turns=${turns:-20}
  errmax=$(header "$f" tool-errors); errmax=${errmax:-3}
  tmodel=$(header "$f" model)
  if [ -n "$tmodel" ] && ! grep -qxF -e "$tmodel" <<<"$allowed"; then die "$base names the model '$tmodel', which is not one of the pinned models ($(tr '\n' ' ' <<<"$allowed"))"; fi
  seed=$(header "$f" seed)
  prompt=$(body "$f")
  if ! prep=$(make_kb "$base" "$seed" 2>&1); then
    echo "FAIL $base: cannot prepare its KB: $(tail -n 1 <<<"$prep")"
    fails=$((fails + 1))
    continue
  fi
  # make_kb ran in a subshell: its exports are lost, so the variables are set here.
  USAGE_KB=$scratch/kb-$base/kb
  USAGE_SEED_COMMITS=$(git -C "$USAGE_KB" rev-list --count HEAD)
  export USAGE_KB USAGE_SEED_COMMITS
  write_mcp_config "$scratch/mcp-$base.json"
  out=$scratch/$base.jsonl
  USAGE_MODEL=${tmodel:-$MODEL}
  start=$(date +%s)
  run_agent "$base" "$prompt" "$scratch/mcp-$base.json" "$out" "$turns"
  rc=$?
  secs=$(($(date +%s) - start))
  if [ "$rc" = 75 ]; then
    echo "agent stderr: $(head -c 300 "$out.err" 2>/dev/null)"
    not_run "$base: the agent could not run or the model is unavailable (transcript $out)"
  fi
  init=$(init_of "$out")
  metrics=$(jq -s -c '
    (map(select(.type == "result")) | last // {}) as $r
    | [.[] | select(.type == "assistant") | .message.content[]? | select(.type == "tool_use")] as $calls
    | [.[] | select(.type == "user") | .message.content[]? | select(.type == "tool_result")] as $res
    | ([range(0; $res | length)] | map(select($res[.].is_error == true))) as $errs
    | { calls: ($calls | length), names: ($calls | map(.name)),
        errors: ($errs | length),
        recoveries: ($errs | map(select(. as $i | $res[$i + 1:] | any(.is_error != true))) | length),
        turns: ($r.num_turns // ([.[] | select(.type == "assistant")] | length)),
        tin: (($r.usage.input_tokens // 0) + ($r.usage.cache_creation_input_tokens // 0) + ($r.usage.cache_read_input_tokens // 0)),
        tout: ($r.usage.output_tokens // 0), result: ($r.result // ""), agent_error: (($r.is_error // false) == true) }' "$out")
  jq -r .result <<<"$metrics" >"$scratch/$base.result"
  calls=$(jq -r .calls <<<"$metrics"); errs=$(jq -r .errors <<<"$metrics"); recov=$(jq -r .recoveries <<<"$metrics")
  tin=$(jq -r .tin <<<"$metrics"); tout=$(jq -r .tout <<<"$metrics"); tturns=$(jq -r .turns <<<"$metrics")
  reasons=
  unknown=$(jq -r --argjson init "${init:-null}" '[.names[] | select(. as $n | (($init.tools // []) | index($n)) == null)] | unique | join(",")' <<<"$metrics")
  [ -z "$unknown" ] || reasons+="called a tool the server did not list: $unknown; "
  [ "$errs" -le "$errmax" ] || reasons+="$errs tool errors, over the ceiling of $errmax; "
  [ "$tturns" -le "$turns" ] || reasons+="$tturns turns, over the ceiling of $turns; "
  [ "$(jq -r .agent_error <<<"$metrics")" != true ] || reasons+="the agent ended with an error ($(jq -r '.result' <<<"$metrics" | head -c 120)); "
  chk=$(USAGE_KB=$USAGE_KB USAGE_SEED_COMMITS=$USAGE_SEED_COMMITS USAGE_RESULT=$scratch/$base.result USAGE_TRANSCRIPT=$out bash "${f%.md}.check.sh" 2>&1) || reasons+="check: $(head -n 1 <<<"$chk"); "
  if [ -z "$reasons" ]; then verdict=PASS; else verdict=FAIL; fails=$((fails + 1)); fi
  echo "$verdict $base calls=$calls errors=$errs recoveries=$recov tokens_in=$tin tokens_out=$tout seconds=$secs${reasons:+ — ${reasons%; }}"
  rows+=("$base | $verdict$(mdcell "${reasons:+: ${reasons%; }}") | $calls | $errs | $recov | $tin | $tout | $secs")
done
[ "$ran" -gt 0 ] || die "no task matched (TASKS=${TASKS:-}) in $TASKS_DIR"

summary_end
if [ "$fails" -gt 0 ]; then
  echo "usage-test: $fails of $ran task(s) failed; transcripts in $scratch"
  exit 1
fi
echo "usage-test: $ran task(s) passed"
if [ -z "${KEEP:-}" ]; then
  case "$scratch" in /*/usage-test.*) [ ! -L "$scratch" ] && rm -rf "$scratch" ;; esac
else echo "usage-test: scratch kept at $scratch"; fi
exit 0
