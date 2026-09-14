---
topic: sync-provisioning
---

# D55 — Agents also materialized on OpenCode, via frontmatter translation

**Context.** D48 materializes `kind: agent` only on `claude` — but OpenCode has its own native
subagents (`.opencode/agent/<nome>.md`, singular dir, incompatible frontmatter: `description` +
`mode: subagent`, name from the filename).

**Decision.** `destDir` maps `agent`×`opencode` → `.opencode/agent/<nome>.md` (codex/kiro
remain `unsupported`). The content is not copied verbatim: `translateAgentForProvider` (a pure
function, called by `Apply` before writing) extracts `description` from the source Claude
frontmatter and emits a minimal OpenCode frontmatter + verbatim body. Claude-only fields that
cannot be mapped reliably (`tools`, `model`, `name`) are **dropped**, not guessed.
`ContentHash` remains that of the source (unchanged): translation happens only at write time,
never in the hash computation — drift detection stays agnostic to the destination provider.
**Rationale.** The translation lives in `Apply` (client-side), not in the manifest, the same
architectural choice as D48. Dropping unmappable fields avoids silently wrong behavior
on an artifact that drives an agent's behavior.
**Discarded alternatives.** Mapping `tools`/`model` with a best-effort heuristic: syntax and names
diverge enough to make any automatic mapping fragile and silently incorrect.
Details: `docs/sync.md` §Agents and hooks, `docs/interoperability.md` §Provider capability matrix.
