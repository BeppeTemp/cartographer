---
topic: sync-provisioning
---

# D362 — OpenCode 2.x gets the write-findings hook through a dedicated plugin

**Decision.** On OpenCode 2.x and later the `cartographer-write-findings` hook (D353) is a dedicated generated
plugin, not the generic KB-hook plugin. It subscribes `ctx.tool.hook("execute.after")`, acts only when the event is
`completed`, `result.output` is a string and `event.tool` ends in `_<write tool>`, pipes `{tool_name, tool_response}`
to the hook's shim on stdin, and on exit 2 appends the stderr text to `event.result.output`. It never throws.
`SupportsWriteFindingsHook(opencode)` is true only when the installed major is 2 or more; on 1.x or an unreadable
version nothing is installed and a previous install is pruned like an opt-out. The subcommand accepts the
`<server>_<tool>` tool name as well as `mcp__<server>__<tool>`, and reads a string holding several JSON values
(the write response followed by the server's sync-state block).

**Why.** Probed on OpenCode 2.0.25 (#638): `execute.after` fires for the code-mode `execute` tool and for each
inner MCP call, named `<server>_<tool>`, whose `result.output` is the MCP result text with the sync-state block
joined on a second line. Appending to that string reaches the model and keeps the result (one write). A throw
reaches it as a tool error and the agent retries the write (7 hook calls against 3), losing its `content_hash`,
the same trap as Codex exit 2 (D361). The generic plugin cannot do this: it passes no stdin, signals by throwing,
and its substring matcher never matches `<server>_<tool>`.

**Alternatives rejected.** Extending the generic plugin with a per-hook mode: it would carry a second
behaviour behind one generator, for one hook. Throwing, as the generic plugin does: retry loop. A
`--channel` for OpenCode: the plugin reads stderr on exit 2 already, and the shim stays identical to Claude's.
Installing on 1.x by analogy: not probed, so declared absent.

**Consequences.** OpenCode 2.x machines get a new plugin file on the next sync. The plugin file's regex is built
from `WriteFindingsTools`, so a new write tool needs no change here. A write response whose JSON the model reads
is unchanged; only text is appended. OpenCode 1.x is added by a probe, not by inference.
