---
topic: concurrency-git
---

# D272 — Writes carry an optional reason, stored as a `Reason:` commit trailer

**Decision.** Every git-wrapped write tool accepts an optional `reason` string.
`gitWrap` adds it to the tool's schema and appends it to the commit as a
`Reason: <text>` trailer after a blank line; the subject is unchanged.
`changes_since` returns the distinct reasons per concept as `reasons`.

**Why.** History recorded what changed (`concept_write: infra/dns`) and never
why, so a return-to-work digest was a list of IDs. A trailer keeps every consumer
of subjects working and is parseable by git itself
(`%(trailers:key=Reason,valueonly)`), with no custom body grammar. Injecting it
centrally leaves the handlers untouched, which ignore unknown keys anyway.
The reason is normalised (trimmed, whitespace collapsed, 500 bytes at a rune
boundary) so one write cannot produce a multi-paragraph body.

**Alternatives rejected.**
- A `reason` argument edited into each handler: dozens of edits, and a future tool
  would forget it. The registry test now catches that instead.
- Putting the reason in the subject: breaks parsers of subjects and `ops`.
- Recording it in the audit log: its field list is a leakage allow-list and free
  text may carry anything.
- Appending it to `log.md`: that file stays the curated narrative.

**Consequences.** `Tool.GitWrapped` marks the tools that got the property; a tool
that already declares `reason` keeps its own schema entry. `gitx.CommitChanges`
has a `Reason` field (first trailer only) and the `LogNameStatus` format has a
fifth NUL-separated field, which later readers of the log build on.
`changes_since` keeps at most 5 reasons per concept.
