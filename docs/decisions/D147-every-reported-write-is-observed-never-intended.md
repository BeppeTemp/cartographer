---
topic: client-configurator
---

# D147 — Every reported write is observed, never intended

Two lines claimed work that had not happened. `connect --dry-run` printed `[kiro] wrote
.kiro/skills/…/SKILL.md` and `connected: kiro` for files it did not write, because `Apply` fills
`AppliedResult.Written` with what it *would* write and `printApplySummary` never received the flag —
while `printConnectResult`, one frame above, branched on it correctly for its own two loops. And
`connect hermes` printed `wrote MCP entry cartographer` immediately before the warning explaining
that Hermes' MCP configuration is never written by Cartographer (D141), because the line was
rendered from `entryNames(entries)` — the entries built from the client config, independent of which
providers can accept them.

Neither was a functional defect: the files on disk were right in both cases. The cost is
epistemic. `connect` is the command whose output *is* its result — run once, in an unfamiliar setup,
with nothing to compare against — and `--dry-run` exists precisely to be read before committing. A
line saying `wrote` that corresponds to no write is worth less than no line at all, and in the
Hermes case it argued against the warning printed to correct it, arriving first.

**The rule.** A `wrote`/`pruned`/`restored`/`connected` line is emitted from the code path that
observed the effect, never from the one that planned it. Mechanically: `printApplySummary` takes
`dryRun` and renders the conditional (`would write`), suppressing the hook-registration line, which
reports a side effect on a shared file that a dry run does not perform either; `sync --dry-run` ends
with `would sync to revision`; and both the MCP-entry lines and the restart hint are scoped by
`providersManagingMCP`, so a mixed `connect claude,hermes` names only claude in the hint.

`connect` and `sync` both build those entries from the client config, so the scoping lives in one
`printMCPEntryLines` they share — the first cut fixed only `connect` and left `sync` announcing the
entry on a Hermes-only client, which is the duplication that caused the defect reasserting itself.

Rejected: dropping the MCP-entry line whenever `ConfigsWritten` is empty. It reads as equivalent but
couples an MCP claim to an unrelated emptiness — a provider that writes its entry into a file
already containing it would lose the line for the wrong reason. The predicate is "does this provider
have an emitter", so that is what the code asks.
