---
topic: control-plane
---

# D65 — "agent" tool profile and compact instructions: less fixed context per session

**Context.** Every agent session paid ~4.8k tokens of fixed cost: 32 tools in `tools/list`
(~3.4k tokens of schemas) and an instructions block in CLAUDE.md of ~1.4k tokens, of which ~500 were
the **duplicated** agent descriptions (already injected by the client's agent registry, where
the agent is installed natively) and the page counts were mutable state inside an imprinting
artifact. Of the 32 tools, only ~half serves the agent in a normal session: the rest is
operator governance/maintenance or plumbing called by name from the client CLI
(`sync_pull` from `connect`/`sync`/`status`, `sync_check` from the SessionStart hook), which does not go
through `tools/list`.

**Decision.**
1. **Tool profile** (`tools.profile` YAML / `CARTOGRAPHER_TOOLS_PROFILE` / `--tools-profile`,
   default **`agent`**): `tools/list` exposes only the core set (17 tools: read/search/write,
   content structure, plus `conflicts_list`+`git_conflict_resolve` for the auto-recovery of the
   `kb-conflict-resolve` skill); the 15 advanced tools (`advancedToolNames`,
   `internal/mcpserver/visibility.go`, marked **[A]** in `control-plane.md`) are hidden but
   remain **callable via `tools/call`** — visibility is not authorization, which stays with
   scopes/RBAC. `profile: full` restores the complete list. The `Server` zero-value = full
   (no surprise for library users); the `agent` default lives in `config.Default()`.
   Golden test `TestServer_ToolsProfile`: every new tool must be classified or the test fails.
2. **Compact instructions** (`generateKBInstructions`): archives as inline names only (no
   page counts — the hash no longer changes with every page added), operational instructions from 6 to
   3 lines, agents as **names only** (the descriptions stay in the `kind: agent` artifact,
   translated and installed natively per provider). Auto-generated block: from ~680 to <100 tokens.

Measured result: fixed cost per session from ~4.8k to ~1.9k tokens (17 tools ≈ 1.86k of schemas
+ reduced block), with the daily flow unchanged (search → read → write → log).

**Amended by D123.** `validate`, `lint`, `gate_check`, and `kb_status` moved
from the advanced set into the `agent` profile's core set: they are
read-only governance the documented agent loop depends on, and a
descriptor-bound MCP host cannot call a tool `tools/list` never advertised.
The counts above are the state as decided here, not current; see D123 for
the measured cost of the change and `control-plane.md` for the current
core/advanced split.

**Discarded alternatives.** Consolidating governance into a single `kb_admin(action=...)` (fewer
tools but a more opaque umbrella schema, and it does not solve the plumbing); filtering `tools/list` by token
scope (entangles visibility and authorization: an rw agent token would still see everything);
also hiding `conflicts_list`/`git_conflict_resolve` (it would break auto-recovery: a client
cannot call unlisted tools); exposing the advanced tools only when conflicts exist via
`notifications/tools/list_changed` (elegant, but non-Claude providers do not re-fetch
reliably).
