---
topic: sync-provisioning
---

# D363 — Crush gets PreToolUse hooks and project mcp/hook cells

**Decision.** KB hooks reach Crush through the `hooks` key of `~/.config/crush/crush.json`: the files go to
`.config/crush/hooks/<name>/` and one entry `{name: "cartographer-<hook>", matcher, command}` is upserted under
`hooks.PreToolUse`, found and removed by that `name`; every other entry and key is kept. Only `PreToolUse` is
registered, any other event installs the files and warns. The registered matcher is the KB matcher prefixed with
`(?i)`. Crush has no session event, so `noSessionStartEvent` is set: no bootstrap hook, the sync trigger stays the
timer, and the write-findings hook is not offered. In a workspace the `mcp` cell is the project `.crush.json` and
the `hook` cell is `.crush/hooks/<name>/` registered in that same file; `agent` and `instructions` stay unsupported.
This amends D225, which marked the hook and the project cells unsupported.

**Why.** Probed on Crush v0.98.0 (#478): `hooks` in the global `crush.json` and in a project `.crush.json` fire, and
of six events only `PreToolUse` does; exit 2 blocks the tool and its stderr reaches the model. A project `.crush.json`
`mcp` entry is listed by the session. Tool names are lowercase (`bash`), the KB's matchers are Claude's (`Edit|Write`),
and Crush is Go, so `(?i)` makes the same alternation match.

**Alternatives rejected.** Matching the KB matcher verbatim: never fires on `Edit`. Lower-casing it: breaks a
character class or a regex escape (`\S`). Registering other events by analogy: not probed, would be a silent no-op.
Deleting `.crush.json` or `crush.json` when the last hook goes: it is the user's configuration (only the project
file, emptied of our `mcp` entry, is removed, as for every JSON provider). Mapping the payload fields of Claude's
`tool_input` onto Crush's: probed for `bash` only, so a hook reading `tool_input.file_path` is its author's concern.

**Consequences.** `PreToolUse` KB hooks now reach Crush on the next sync. `registerMCPServer` takes the scope and
writes the project cell of the destination matrix in a workspace; `removeMCPServer` takes the managed path, and
`mcpProviderFromPath` recognises `.crush.json` and `.mcp.json`. A tracked `.crush.json` makes the workspace hygiene
check refuse, as for `.mcp.json`. A new Crush event or tool-feedback channel is added by a probe, not by inference.
