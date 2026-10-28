---
topic: data-plane
---

# D1313 — A nested frontmatter value is refused, never written as text

**Decision.** OKF frontmatter stays the flat YAML subset. A value that is a map, or a list holding a map or a list, is refused by every write tool (`concept_write`, `concept_batch`, `concept_patch`) with `invalid_frontmatter`, naming the key and saying nested values are not supported (flatten the keys or put the structure in the body). `serializeEntry` returns an error for a type it does not know; it has no `%v` default branch.

**Why.** A map printed with `%v` became the string `map[a:1]`: the write answered `findings: []` and the agent reported nested data as stored. Silent data corruption is worse than a refusal, and the refusal tells the agent what to do.

**Alternatives rejected.** Serializing nested values as YAML: the parser, lint and the round-trip check are built on the flat subset (D291 keeps only opaque blocks), so it would widen the format for every reader. Coercing to JSON text: a string that looks structured is the same lie in a different form.

**Consequences.** `applyFrontmatterMap` validates before touching the frontmatter, and `WriteConcept` runs `Frontmatter.Validate` as a second guard. A future new frontmatter type must be added to `serializeEntry` explicitly.
