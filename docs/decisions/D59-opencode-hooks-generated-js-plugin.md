---
topic: sync-provisioning
---

# D59 — OpenCode hooks: generated JS plugin

**Context.** After D57/D58, `kind: hook` remained `Unsupported` on OpenCode: no declarative
hooks to register in a file — OpenCode instead loads JS/TS plugins from
`~/.config/opencode/plugins/` (auto-loaded, no entry in `opencode.json`).

**Decision.** `destDir("hook", _, opencode)` materializes the files in `.opencode/hooks/<nome>/`;
`Apply` generates — if the KB event is mappable (`PreToolUse`→`tool.execute.before`,
`PostToolUse`→`tool.execute.after`, `SessionStart`/`Stop` on the generic pub/sub bus filtered by
`event.type`, other events → no plugin) — an entire deterministic plugin file
`cartographer-<nome>.js` that runs the materialized script. **Per-file ownership, not
per-block**: unlike D57/D58, the generated file belongs entirely to Cartographer — rewritten
in full on update, deleted on prune, no block parsing. Unmappable event →
`AppliedResult.Warnings`, not an error. The matcher (only `PreToolUse`/`PostToolUse`) uses a
bidirectional case-insensitive substring comparison (a heuristic, not a guaranteed tool-name table).
**Rationale.** One file per hook is consistent with the existing skill/agent scheme and makes prune
trivial (`os.Remove`), discarding a single "router" plugin that would be harder to maintain incrementally.
**Open question.** The substring matcher is a heuristic: a hook with a very specific matcher
might not behave identically on the two providers.
Details: `docs/sync.md` §Agents and hooks.
