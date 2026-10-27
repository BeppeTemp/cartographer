---
topic: control-plane
---

# D1185 — Tool results are lighter by default: compact JSON, `search` limit 8 and no `related`, no `frontmatter_raw`

**Decision.** Every JSON tool result is built by `jsonResult`, compact (no indentation), and a test forbids `MarshalIndent` in `internal/mcpserver`. `search` returns 8 hits by default and a `related` block only for `related: N`. `concept_read` sends `frontmatter_raw` only when the frontmatter does not parse; the exact bytes are `content` under `with_content`.

**Why.** Measured on 20 real agent sessions, a `search` answer weighed 4.2 KB on average: the `related` block was 19% of it and the indentation 18%. The same questions asked with `limit: 8` and `related: 0` averaged 2.2 KB, 20 of 20 on a blind-judged answer set, with no loss of quality. In `concept_read` the frontmatter travelled twice (3.5%). The saving a client sees comes from fewer calls, not from less text per call, so the plan trims only what no answer needs and adds no nudge to read by section. The price is a breaking change for a client that relied on the old defaults or on `frontmatter_raw` in a default read.

**Alternatives rejected.**
- Compacting in `callTool` whatever looks like JSON: some tools return stored text verbatim (`index_get` without `with_hash`, artifact and asset content), and a stored file that happens to be valid JSON must stay byte-identical; compaction is at construction.
- A `with_raw` flag on `concept_read`: one more parameter in every agent's `tools/list` for a field nothing reads.
- Dropping `frontmatter_raw` in every case: loses the repair path of a page whose frontmatter does not parse.
- Clamping an explicit `search` limit: not needed, the Atlas endpoint has its own default and clamp.

**Consequences.** Amends D185 and D309 (`frontmatter_raw` is no longer unconditional), D781 (the default of `related` is 0; the parameter stays) and D70 (snippets were sized for limit 20). `TestToolResultGolden` proves a result equals its golden up to whitespace; `TestSearchDefaultAnswerBudget` keeps the default `search` answer under 2.5 KB on average, and if it fails the number is not raised; the generated guidance never tells an agent to pass `limit` or `related` to `search` or to read runbooks, procedures and incidents by section. The Atlas search endpoint keeps its own `limit` (20, clamped to 100) and `related: 0`.
