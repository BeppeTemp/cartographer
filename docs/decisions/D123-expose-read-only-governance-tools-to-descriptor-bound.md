---
topic: control-plane
---

# D123 — Expose read-only governance tools to descriptor-bound agents

**Status: implemented (2026-08-04).**

**Context.** The default `agent` tools profile (D65) hid `validate`, `lint`,
`gate_check`, and `kb_status` from `tools/list`. They stayed registered and
directly callable by name (`ToolAdvanced` filters `tools/list`, not
dispatch), which is enough for a stdio/TUI agent that can invoke any known
tool name. It is not enough for a descriptor-bound MCP host such as Codex,
which can only invoke tools `tools/list` advertises: the documented agent
loop (`docs/loop.md` §Validate and lint, `docs/use-cases.md`) requires all
four, but a descriptor-bound agent had no way to call them.

**Decision.** Remove `validate`, `lint`, `gate_check`, and `kb_status` from
`advancedToolNames` (`internal/mcpserver/visibility.go`): they join the
`agent` profile's core set, unchanged in every other respect — same
`ReadOnly: true` classification (`readonly.go`), same `resourceWhole`
resource class and whole-KB RBAC (`policy.go`), same handlers. Everything
else stays advanced: `commit_gate`, contradiction/conflict mutation, index
maintenance, provisioning, secrets, artifact enumeration/removal, and PR
finalization remain operator-only, because they are either mutating or
genuinely niche — unlike these four, no documented agent-loop step depends
on a descriptor-bound host being able to invoke them without an operator
switching the server to `profile: full`.

**Measured cost.** Serialized `tools/list` under the `agent` profile against
the demo KB: 28 tools / 21814 bytes (~5453 tokens, bytes/4 estimate per D65)
before, 32 tools / 23324 bytes (~5831 tokens) after — a ~378-token, ~7%
increase for the four added descriptors, well short of erasing D65's
~2.9k-token reduction from the pre-D65 baseline.

**Rationale.** No third profile and no umbrella governance tool: a third
profile duplicates the two-value contract admin/operators already reason
about (`agent`/`full`) without resolving the descriptor-bound gap for the
existing default, and an umbrella tool (`kb_admin(action=...)`, already
discarded once by D65) trades four typed, independently discoverable
schemas for one opaque one. Reclassification also does not touch RBAC:
`ToolAdvanced` gates `tools/list` visibility only, `authorizeTool` and the
resource-class tables are untouched, so a caller who could not call these
four before still cannot call them after — only what a compliant
descriptor-bound client is told exists has changed.
