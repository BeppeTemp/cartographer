---
topic: sync-provisioning
---

# D361 — Codex gets the write-findings hook, through `additionalContext` on stdout

**Decision.** The `cartographer-write-findings` hook (D353) is installed for Codex too. The feedback channel is
chosen by an argument of the subcommand, never by sniffing the payload: `cartographer hook write-findings
--channel stderr|context`. `stderr` (default, Claude Code) prints the message on stderr and exits 2; `context`
(Codex) prints `{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"<message>"}}` on
stdout and exits 0, never 2. The hook JSON is per provider (`writeFindingsCommandArgs`): Codex's command is
`./write-findings.sh --channel context`, and the shims forward their arguments (`"$@"`, `%*`). A bad flag is
exit 0, not a usage error.

**Why.** Probed on Codex 0.162.0 (#637): the payload carries `tool_name` (`mcp__<server>__<tool>`) and
`tool_response` as the MCP result object, which the parser already reads. Exit 2 on a `PostToolUse` hook
replaces the tool result with the stderr text; the agent reads a failed call and repeats the write, losing the
`content_hash`. `additionalContext` with exit 0 reaches the model and keeps the result. Exit 0 with stderr
only does not reach the model.

**Alternatives rejected.** Sniffing `turn_id` in the payload to pick the channel: an undocumented field is not
a contract. Using `additionalContext` for Claude Code too: D353 verified stderr + exit 2 there and this probe
did not re-run it. A separate subcommand per client: the parsing and the message are shared. Leaving Codex
out: the agent would never see findings on that client.

**Consequences.** The hook JSON, and so the content hash, changes for Claude installs only through the
shim's `"$@"`: existing installs are rewritten once on the next sync. Codex trusts hooks per definition, so
the user approves the new hook once. A new client adds a `writeFindingsCommandArgs` entry and a
`writeFindingsHook: true` declaration, after a probe of its channel.
