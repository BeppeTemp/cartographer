---
topic: control-plane
---

# D343 — MCP denials name a reason, reads expose the vocabularies, search rescues thin AND results

**Decision.** A policy denial reads `forbidden: <code>: <hint>` (codes: `unclassified_tool`, `bad_arguments`, `read_only_token`, `needs_whole_kb`, `outside_scope`, `template_unusable`, `missing_id`, `no_kb_access`, `no_principal`). `map_list` returns each map's `field_values` and `field_values_by_type`. `concept_list` returns `status` and, on request, up to 8 frontmatter `fields`. `search` runs the OR pass whenever fewer than `search.OrFallbackFloor` (3) allowed full matches exist for a query of two or more terms, appends the OR-only hits after every full match, and marks them (`partial`, `or_fallback`, `note`).

**Why.** The reason for a denial was known at each site and thrown away, so an agent could not tell a read-only token from a scope problem. A pre-write tag refusal contradicts "lint contract, not a write gate" and is owned by the opt-in gate (D350); exposing the vocabulary lets a client check first at no tool-description cost. The OR fallback only fired on zero full matches, so a query with one hit hid the near misses. A floor of 3 leaves a precise query alone and rescues the thin case; appending keeps the AND ranking intact.

**Alternatives rejected.**
- Gate tags before the write: duplicates D350 and breaks the contract-not-gate rule.
- Reasons on the `genericNotFound` sites: would confirm a hidden concept exists. A token with no grant on the KB at all also gets `not found`, not `read_only_token`, for the same reason.
- Merging OR hits by score: an OR-only hit with a high term frequency would outrank a full match.
- `fields` on `work_list` too: out of scope, it stays scalar-only.

**Consequences.** The `forbidden` prefix must stay (clients match on it); a new denial site picks a code from the list. Hints describe the caller's own token and arguments, never KB content, and a batch denial never names the operation. The partial-last sort runs after the centrality prior. `graph_context` seeds from the same hits and so sees the OR-only ones.
