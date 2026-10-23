---
topic: project-governance
---

# D1125 — The tool table is checked against the registry

**Decision.** `docs/control-plane.md` §MCP API stays hand-written prose, and `internal/mcpserver/docs_test.go` checks it against the registry in both directions: `TestToolTableMatchesRegistry` (one row per registered tool, every row a registered tool, `**[R]**` iff `Tool.ReadOnly`, `**[A]**` iff `ToolAdvanced`, no other marker, `**[R]**` before `**[A]**`) and `TestToolTableParametersMatchSchemas` (the row's parameters against the top-level `properties` and `required` of the tool's `InputSchema`). The tool-name scan of D101 also reads the bundled skills, `.claude/` and the area `AGENTS.md` files, and lint check and observation names are exempt through `lint.Checks()` / `lint.Observations()` instead of a hand list.

**Why.** D101 only caught doc to code. A registered tool with no row, a wrong marker or a signature that drifted from the schema was invisible, although the table is declared the source of truth. Descriptions are curated and longer than the `tools/list` budget, so generating the table was not an option.

**Alternatives rejected.** Generating the table from the registry: loses the curated descriptions. Comparing nested schemas (`items`, `moves[]` entries): the grammar has no notation for them. Defining `**[RW]**`: the read/write split is already `[R]` versus the rest.

**Consequences.** A new or changed tool fails the gate until its row says the truth; the messages name the tool and the fix. `signatureAllow` (reason mandatory, stale entries fail) exempts only the required-ness of a row the schema cannot express (`concept_move`); `kb`, and `reason` on a git-wrapped tool, are never compared.
