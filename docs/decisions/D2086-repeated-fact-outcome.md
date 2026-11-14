---
topic: control-plane
---

# D2086 — A page's own status line is not a repeated fact

**Decision.** `repeated_fact` and the write-time advice skip a page's leading blockquote above its first H2 whatever the status, and, on a page with a finished status, its first plain paragraph line (not a list item, table row, code fence or HTML) above the first H2. Finished is `kb.IsRetired`, the done, resolved or deprecated value family (the map contract's `value_synonyms` extend it), or `terminalOutcomes` in `internal/lint/review_cost.go` (cancelled, rejected, abandoned, wontfix, merged and Italian equivalents). The same line below an H2 is still a fact.

**Why.** Pages record their own outcome ("Closed without running it on <date>", "Merged on <date> into ...", an index-page banner); linking one concept does not fit, each line is that page's state. #1194 exempted only the banner of an archived entry, so the same shape on live or done pages kept raising items nobody could act on. A position rule covers every wording; a phrase list would not.

**Alternatives rejected.** A phrase list: wording is the KB's own. Exempting the whole first paragraph: a long first paragraph can hold a real shared fact. Exempting the first line on live pages too: on an open page it is usually prose, not an outcome.

**Consequences.** A fact a finished page states as its first line is no longer reported if repeated; it must sit below an H2 to count. The bundled kb-doctor skill text and docs/data-plane.md state the new rule.
