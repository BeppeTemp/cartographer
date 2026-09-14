---
topic: data-plane
---

# D62 — KB repo cleanup: no generated AGENTS.md/.gitignore, exclude via .git/info/exclude (WP6)

**Context.** `kb.Init` generated, besides the content skeleton, `AGENTS.md` (D19,
soft agent contract) and `.gitignore` (`.cartographer/`). Both were noise: `AGENTS.md`
presupposed an agent editing the KB directly on the filesystem (nonexistent scenario, the KB is
always mediated by the server); `.gitignore` versioned a purely local need.

**Decision.** `kb.Init` no longer writes `AGENTS.md` or `.gitignore`. Excluding
`.cartographer/` moves to **`.git/info/exclude`** (`ensureInfoExclude`, git's local file, never
versioned), called both by `Init` and by `Open` — so every existing KB self-migrates at the first
`Open` with no operator intervention.
**Backward compatibility.** `AGENTS.md` remains in `reservedNames` (D19): pre-D62 KBs with these files
keep working unchanged, no migration removes them.
**Discarded alternatives.** Removing existing AGENTS.md/.gitignore on Open (destructive on
content potentially edited by hand); a `.gitignore` kept but unversioned via merge
strategy (`.git/info/exclude` is already git's native mechanism for this).
Details: `docs/data-plane.md` §Filesystem layout of a KB.
