---
topic: sync-provisioning
---

# D1614 — Pi hooks are one generated extension per hook, with their files outside `extensions/`

**Decision.** Pi has no hook file, so each KB hook is a deterministic extension
`~/.pi/agent/extensions/cartographer-<name>.ts` that Cartographer owns whole and returns as a managed file (a prune
deletes it). The hook's own files go to `~/.pi/agent/cartographer-hooks/<name>/`. `SessionStart`, `PreToolUse` and
`PostToolUse` map to `session_start` (every reason), `tool_call` and `tool_result`. The matcher is
`new RegExp(matcher, "i")`, unanchored, tested against the tool name; the command runs as `sh -c` with a JSON payload on
stdin, 60 s, stdout ignored. `PreToolUse` exit 2 returns `{block, reason}`; `PostToolUse` exit 2 appends the stderr as a
text block (returning `structuredContent` too) and the hook is skipped when `isError` is set; every other outcome is
swallowed and the generated code never throws.

**Why.** Probe 4 on Pi 1.5.0 showed a global extension loads without trust and that `tool_call`, `tool_result` and
`session_start` fire. Pi also loads `extensions/*/index.ts`, and a KB hook directory may carry an `index.ts`, so only the
registration may live under `extensions/`. A throw or an error result from a `tool_result` handler makes the agent retry
the write (the trap of D362), so the handler returns a patch and nothing else. The cost is a generated JavaScript file
whose contract (the `tool_result` patch) comes from the vendor docs and was not exercised end to end on a real Pi in this
change.

**Alternatives rejected.** One dispatching shim for all hooks: ownership, prune and the lockfile are per hook, as for the
OpenCode plugin and the Copilot hook file. Patching a settings file: Pi has none for hooks. Registering `Stop` or
`UserPromptSubmit` by analogy: not probed, and a destination is declared, never invented. An anchored, case-sensitive
matcher: Pi's built-ins are lowercase while the KB's matchers are Claude's.

**Consequences.** `hookEventReach` and `hookCapableProviders` list Pi for the three events, so `SupportsSessionHook` is
true and no sync timer is advised. The `tool_result` return, the `PreToolUse` block and the bootstrap hook in `pi -p`
still need a probe on a real Pi (`docs/harnesses.md ## pi`). A change to Pi's extension API or its loader is a harness
change, not a Cartographer bug.
